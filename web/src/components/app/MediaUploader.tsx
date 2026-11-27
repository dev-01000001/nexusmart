"use client";

import React, { useEffect, useId, useRef, useState } from "react";
import {
  AlertTriangle,
  ArrowLeft,
  ArrowRight,
  ImagePlus,
  Loader2,
  Star,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { cn } from "@/lib/utils";
import {
  MEDIA_ACCEPT_ATTR,
  MEDIA_MAX_PRODUCT_IMAGES,
  appendUniqueMediaUrl,
  formatMediaBytes,
  mapMediaUploadError,
  reorderMediaUrls,
  setPrimaryMediaUrl,
  validateMediaFileClient,
  type AllowedMediaMime,
} from "./mediaUploadUtils";

export interface UploadedMediaInfo {
  url: string;
  width: number;
  height: number;
  bytes: number;
  content_type: string;
}

interface UploadQueueItem {
  id: string;
  name: string;
  size: number;
  previewUrl: string;
  progress: number;
}

const DRAG_INDEX_MIME = "text/x-nexusmart-media-index";

interface UploadMediaApiResponse {
  media?: UploadedMediaInfo;
  error?: { code?: string; message?: string };
}

function uploadSellerMediaFile(
  file: File,
  contentType: AllowedMediaMime,
  onProgress: (percent: number) => void
): Promise<UploadedMediaInfo> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/seller/media", true);
    xhr.withCredentials = true;
    xhr.setRequestHeader("Content-Type", contentType);
    xhr.setRequestHeader("Accept", "application/json");

    xhr.upload.onprogress = (evt) => {
      if (evt.lengthComputable && evt.total > 0) {
        onProgress(Math.min(99, Math.round((evt.loaded / evt.total) * 100)));
      }
    };

    xhr.onload = () => {
      let parsed: UploadMediaApiResponse | null = null;
      try {
        parsed = JSON.parse(xhr.responseText) as UploadMediaApiResponse;
      } catch {
        // Non-JSON response body
      }

      if (xhr.status >= 200 && xhr.status < 300 && parsed?.media?.url) {
        onProgress(100);
        resolve(parsed.media);
        return;
      }

      const fallbackMsg = mapMediaUploadError(xhr.status, parsed?.error?.code);
      reject(new Error(parsed?.error?.message?.trim() || fallbackMsg));
    };

    xhr.onerror = () => {
      reject(new Error("Kết nối gián đoạn khi tải ảnh lên. Vui lòng thử lại."));
    };

    xhr.onabort = () => {
      reject(new Error("Đã hủy thao tác tải ảnh lên."));
    };

    xhr.send(file);
  });
}

export interface MediaUploaderProps {
  value: string[];
  onChange: (next: string[]) => void;
  maxFiles?: number;
  disabled?: boolean;
  className?: string;
}

/**
 * Multi-image uploader for Seller Center (drag-and-drop, multi-select up to `maxFiles`,
 * real upload progress, preview, drag or button reorder, "Đặt làm ảnh chính", remove,
 * and Vietnamese error messages).
 */
export function MediaUploader({
  value,
  onChange,
  maxFiles = MEDIA_MAX_PRODUCT_IMAGES,
  disabled = false,
  className,
}: MediaUploaderProps) {
  const inputId = useId();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const latestUrlsRef = useRef<string[]>(value);
  latestUrlsRef.current = value;
  const activeObjectUrlsRef = useRef<Set<string>>(new Set());

  const [queue, setQueue] = useState<UploadQueueItem[]>([]);
  const [dragActive, setDragActive] = useState(false);
  const [draggedIdx, setDraggedIdx] = useState<number | null>(null);
  const [dropTargetIdx, setDropTargetIdx] = useState<number | null>(null);
  const [errors, setErrors] = useState<string[]>([]);

  useEffect(() => {
    const objectUrls = activeObjectUrlsRef.current;
    return () => {
      for (const blobUrl of objectUrls) {
        URL.revokeObjectURL(blobUrl);
      }
      objectUrls.clear();
    };
  }, []);

  const totalPending = queue.length;
  const remainingSlots = Math.max(0, maxFiles - value.length - totalPending);
  const isBusy = totalPending > 0;

  const handleSelectedFiles = async (fileList: FileList | File[]) => {
    if (disabled) return;
    const files = Array.from(fileList);
    if (files.length === 0) return;

    const nextErrors: string[] = [];
    const currentCapacity = Math.max(0, maxFiles - latestUrlsRef.current.length - queue.length);
    if (currentCapacity <= 0) {
      setErrors([`Đã đạt tối đa ${maxFiles} ảnh. Hãy xóa bớt ảnh cũ trước khi tải thêm.`]);
      return;
    }

    const accepted: Array<{ file: File; contentType: AllowedMediaMime; item: UploadQueueItem }> =
      [];

    for (const file of files) {
      const check = validateMediaFileClient(file);
      if (!check.ok) {
        nextErrors.push(`${file.name}: ${check.message}`);
        continue;
      }
      if (accepted.length >= currentCapacity) {
        nextErrors.push(
          `Chỉ có thể tải thêm tối đa ${currentCapacity} ảnh nữa (giới hạn ${maxFiles} ảnh).`
        );
        break;
      }
      const previewUrl = URL.createObjectURL(file);
      activeObjectUrlsRef.current.add(previewUrl);
      accepted.push({
        file,
        contentType: check.contentType,
        item: {
          id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
          name: file.name,
          size: file.size,
          previewUrl,
          progress: 5,
        },
      });
    }

    setErrors(nextErrors);
    if (accepted.length === 0) return;

    setQueue((prev) => [...prev, ...accepted.map((a) => a.item)]);

    for (const entry of accepted) {
      try {
        const uploaded = await uploadSellerMediaFile(entry.file, entry.contentType, (pct) => {
          setQueue((prev) =>
            prev.map((q) => (q.id === entry.item.id ? { ...q, progress: pct } : q))
          );
        });
        const next = appendUniqueMediaUrl(latestUrlsRef.current, uploaded.url, maxFiles);
        latestUrlsRef.current = next;
        onChange(next);
      } catch (err) {
        const msg =
          err instanceof Error && err.message
            ? err.message
            : "Không thể tải ảnh lên lúc này. Vui lòng thử lại sau.";
        setErrors((prev) => [...prev, `${entry.file.name}: ${msg}`]);
      } finally {
        URL.revokeObjectURL(entry.item.previewUrl);
        activeObjectUrlsRef.current.delete(entry.item.previewUrl);
        setQueue((prev) => prev.filter((q) => q.id !== entry.item.id));
      }
    }
  };

  const handleDropFiles = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault();
    setDragActive(false);
    if (disabled) return;
    // Ignore internal tile-reorder drags dropped on the outer dropzone.
    if (e.dataTransfer.types.includes(DRAG_INDEX_MIME)) return;
    if (e.dataTransfer.files && e.dataTransfer.files.length > 0) {
      void handleSelectedFiles(e.dataTransfer.files);
    }
  };

  const handleRemove = (targetUrl: string) => {
    if (disabled) return;
    const next = value.filter((u) => u !== targetUrl);
    latestUrlsRef.current = next;
    onChange(next);
  };

  const handleSetPrimary = (targetUrl: string) => {
    if (disabled) return;
    const next = setPrimaryMediaUrl(value, targetUrl);
    latestUrlsRef.current = next;
    onChange(next);
  };

  const handleReorder = (fromIndex: number, toIndex: number) => {
    if (disabled) return;
    const next = reorderMediaUrls(value, fromIndex, toIndex);
    latestUrlsRef.current = next;
    onChange(next);
  };

  return (
    <div className={cn("space-y-3", className)}>
      <div
        onDragOver={(e) => {
          if (disabled || e.dataTransfer.types.includes(DRAG_INDEX_MIME)) return;
          e.preventDefault();
          if (!dragActive) setDragActive(true);
        }}
        onDragLeave={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node | null)) {
            setDragActive(false);
          }
        }}
        onDrop={handleDropFiles}
        className={cn(
          "flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed px-4 py-5 text-center transition-colors",
          dragActive
            ? "border-brand bg-badge-info-bg/40"
            : "border-input bg-muted/35 hover:border-foreground/40",
          disabled && "pointer-events-none opacity-60"
        )}
      >
        <input
          id={inputId}
          ref={inputRef}
          type="file"
          accept={MEDIA_ACCEPT_ATTR}
          multiple
          disabled={disabled || remainingSlots <= 0}
          className="sr-only"
          onChange={(e) => {
            const picked = e.currentTarget.files;
            if (picked && picked.length > 0) {
              void handleSelectedFiles(picked);
            }
            e.currentTarget.value = "";
          }}
        />
        <div className="flex size-10 items-center justify-center rounded-full bg-card text-brand shadow-card">
          {isBusy ? (
            <Loader2 className="size-5 animate-spin" aria-hidden="true" />
          ) : (
            <ImagePlus className="size-5" aria-hidden="true" />
          )}
        </div>
        <div className="space-y-1">
          <p className="text-[13px] font-medium text-foreground">
            Kéo thả ảnh vào đây hoặc chọn tệp từ thiết bị
          </p>
          <p className="text-xs text-muted-foreground tabular-nums">
            Hỗ trợ JPEG, PNG, WebP · Tối đa 5 MB/ảnh · Đã có {value.length}/{maxFiles} ảnh
          </p>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={disabled || remainingSlots <= 0}
          onClick={() => inputRef.current?.click()}
          className="mt-1 min-h-10 sm:min-h-8"
        >
          <Upload className="size-3.5" aria-hidden="true" />
          <span>{remainingSlots > 0 ? "Chọn ảnh tải lên" : `Đã đủ ${maxFiles} ảnh`}</span>
        </Button>
      </div>

      {errors.length > 0 && (
        <Alert variant="destructive">
          <AlertTriangle className="size-4" aria-hidden="true" />
          <AlertDescription className="flex items-start justify-between gap-2">
            <ul className="space-y-1 text-xs">
              {errors.map((msg, idx) => (
                <li key={`${idx}-${msg}`}>{msg}</li>
              ))}
            </ul>
            <Button
              type="button"
              variant="ghost"
              size="icon-xs"
              aria-label="Đóng thông báo lỗi tải ảnh"
              className="shrink-0 text-destructive hover:text-destructive"
              onClick={() => setErrors([])}
            >
              <X className="size-3.5" aria-hidden="true" />
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {(value.length > 0 || queue.length > 0) && (
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4">
          {value.map((url, idx) => {
            const isPrimary = idx === 0;
            const isDropTarget = dropTargetIdx === idx && draggedIdx !== null && draggedIdx !== idx;
            return (
              <div
                key={url}
                draggable={!disabled && value.length > 1}
                onDragStart={(e) => {
                  if (disabled) return;
                  setDraggedIdx(idx);
                  e.dataTransfer.effectAllowed = "move";
                  e.dataTransfer.setData(DRAG_INDEX_MIME, String(idx));
                }}
                onDragOver={(e) => {
                  if (draggedIdx === null || draggedIdx === idx) return;
                  e.preventDefault();
                  e.dataTransfer.dropEffect = "move";
                  setDropTargetIdx(idx);
                }}
                onDragLeave={() => {
                  if (dropTargetIdx === idx) setDropTargetIdx(null);
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  const rawIdx = e.dataTransfer.getData(DRAG_INDEX_MIME);
                  const fromIdx = rawIdx ? Number(rawIdx) : draggedIdx;
                  setDraggedIdx(null);
                  setDropTargetIdx(null);
                  if (typeof fromIdx === "number" && Number.isInteger(fromIdx)) {
                    handleReorder(fromIdx, idx);
                  }
                }}
                onDragEnd={() => {
                  setDraggedIdx(null);
                  setDropTargetIdx(null);
                }}
                className={cn(
                  "group relative flex flex-col overflow-hidden rounded-lg border bg-card transition-all",
                  isPrimary ? "border-brand ring-1 ring-brand" : "border-border",
                  isDropTarget && "ring-2 ring-brand ring-offset-1",
                  draggedIdx === idx && "opacity-50"
                )}
              >
                <div className="relative aspect-square bg-muted">
                  <img
                    src={url}
                    alt={isPrimary ? "Ảnh chính sản phẩm" : `Ảnh sản phẩm ${idx + 1}`}
                    className="size-full object-cover"
                    width={240}
                    height={240}
                  />
                  {isPrimary ? (
                    <Badge variant="info" className="absolute top-1.5 left-1.5 shadow-card">
                      Ảnh chính
                    </Badge>
                  ) : (
                    <span className="absolute top-1.5 left-1.5 rounded-full bg-topbar/80 px-2 py-0.5 text-[11px] font-medium text-white tabular-nums">
                      #{idx + 1}
                    </span>
                  )}
                  <Button
                    type="button"
                    variant="secondary"
                    size="icon-xs"
                    disabled={disabled}
                    aria-label={`Xóa ảnh ${idx + 1}`}
                    onClick={() => handleRemove(url)}
                    className="absolute top-1.5 right-1.5 size-7 rounded-full bg-card/90 text-destructive hover:bg-card hover:text-destructive"
                  >
                    <Trash2 className="size-3.5" aria-hidden="true" />
                  </Button>
                </div>

                <div className="flex items-center justify-between gap-1 border-t border-border bg-card px-1.5 py-1.5">
                  {isPrimary ? (
                    <span className="inline-flex items-center gap-1 px-1 text-[11px] font-medium text-brand">
                      <Star className="size-3 fill-current" aria-hidden="true" />
                      <span>Ảnh bìa</span>
                    </span>
                  ) : (
                    <Button
                      type="button"
                      variant="ghost"
                      size="xs"
                      disabled={disabled}
                      onClick={() => handleSetPrimary(url)}
                      className="h-7 px-1.5 text-[11px] text-foreground"
                    >
                      Đặt ảnh chính
                    </Button>
                  )}

                  {value.length > 1 && (
                    <div className="flex items-center gap-0.5">
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        disabled={disabled || idx === 0}
                        aria-label={`Di chuyển ảnh ${idx + 1} sang trái`}
                        onClick={() => handleReorder(idx, idx - 1)}
                        className="size-7 text-muted-foreground"
                      >
                        <ArrowLeft className="size-3" aria-hidden="true" />
                      </Button>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        disabled={disabled || idx === value.length - 1}
                        aria-label={`Di chuyển ảnh ${idx + 1} sang phải`}
                        onClick={() => handleReorder(idx, idx + 1)}
                        className="size-7 text-muted-foreground"
                      >
                        <ArrowRight className="size-3" aria-hidden="true" />
                      </Button>
                    </div>
                  )}
                </div>
              </div>
            );
          })}

          {queue.map((item) => (
            <div
              key={item.id}
              className="flex flex-col overflow-hidden rounded-lg border border-border bg-card"
              role="status"
              aria-label={`Đang tải ảnh ${item.name} ${item.progress}%`}
            >
              <div className="relative aspect-square bg-muted">
                <img
                  src={item.previewUrl}
                  alt={item.name}
                  className="size-full object-cover opacity-70"
                  width={240}
                  height={240}
                />
                <div className="absolute inset-0 flex items-center justify-center bg-topbar/25">
                  <Loader2 className="size-6 animate-spin text-white" aria-hidden="true" />
                </div>
              </div>
              <div className="space-y-1.5 border-t border-border p-2">
                <div className="flex items-center justify-between gap-1 text-[11px] text-muted-foreground tabular-nums">
                  <span className="truncate">{item.name}</span>
                  <span className="shrink-0 font-medium text-foreground">{item.progress}%</span>
                </div>
                <Progress value={item.progress} className="h-1.5" />
                <div className="text-[11px] text-muted-foreground tabular-nums">
                  {formatMediaBytes(item.size)}
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

export interface SingleMediaUploadButtonProps {
  onUploaded: (url: string, meta: UploadedMediaInfo) => void;
  label?: string;
  disabled?: boolean;
  className?: string;
}

/**
 * Compact single-image upload button (with progress & Vietnamese inline error) for Site Studio
 * logo, banner, and section hero/image URL fields.
 */
export function SingleMediaUploadButton({
  onUploaded,
  label = "Tải ảnh lên",
  disabled = false,
  className,
}: SingleMediaUploadButtonProps) {
  const inputId = useId();
  const inputRef = useRef<HTMLInputElement | null>(null);
  const [uploading, setUploading] = useState(false);
  const [progress, setProgress] = useState(0);
  const [errorMsg, setErrorMsg] = useState<string | null>(null);

  const handleFile = async (file: File) => {
    setErrorMsg(null);
    const check = validateMediaFileClient(file);
    if (!check.ok) {
      setErrorMsg(check.message);
      return;
    }

    setUploading(true);
    setProgress(10);
    try {
      const uploaded = await uploadSellerMediaFile(file, check.contentType, setProgress);
      onUploaded(uploaded.url, uploaded);
    } catch (err) {
      setErrorMsg(
        err instanceof Error && err.message
          ? err.message
          : "Không thể tải ảnh lên lúc này. Vui lòng thử lại sau."
      );
    } finally {
      setUploading(false);
      setProgress(0);
    }
  };

  return (
    <div className={cn("space-y-1.5", className)}>
      <input
        id={inputId}
        ref={inputRef}
        type="file"
        accept={MEDIA_ACCEPT_ATTR}
        disabled={disabled || uploading}
        className="sr-only"
        onChange={(e) => {
          const picked = e.currentTarget.files?.[0];
          if (picked) {
            void handleFile(picked);
          }
          e.currentTarget.value = "";
        }}
      />
      <div className="flex flex-wrap items-center gap-2">
        <Button
          type="button"
          variant="outline"
          size="xs"
          disabled={disabled || uploading}
          onClick={() => inputRef.current?.click()}
          className="h-8 gap-1.5 px-2.5 text-xs"
        >
          {uploading ? (
            <Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
          ) : (
            <Upload className="size-3.5" aria-hidden="true" />
          )}
          <span className="tabular-nums">{uploading ? `Đang tải ${progress}%` : label}</span>
        </Button>
        <span className="text-[11px] text-muted-foreground">JPG, PNG, WebP ≤ 5 MB</span>
      </div>
      {uploading && <Progress value={progress} className="h-1.5" />}
      {errorMsg && (
        <p className="text-xs text-destructive" role="alert">
          {errorMsg}
        </p>
      )}
    </div>
  );
}
