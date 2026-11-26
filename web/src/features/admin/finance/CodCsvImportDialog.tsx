"use client";

import React from "react";
import { Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { FilterSelect, type FilterSelectOption } from "../config/adminUiHelpers";

export interface CodCsvImportDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  carrierOptions: FilterSelectOption[];
  codCarrier: string;
  onCodCarrierChange: (value: string) => void;
  codDate: string;
  onCodDateChange: (value: string) => void;
  codFile: File | null;
  onCodFileChange: (file: File | null) => void;
  codImporting: boolean;
  codError: string | null;
  onSubmit: (e: React.FormEvent) => void;
}

export function CodCsvImportDialog({
  open,
  onOpenChange,
  carrierOptions,
  codCarrier,
  onCodCarrierChange,
  codDate,
  onCodDateChange,
  codFile,
  onCodFileChange,
  codImporting,
  codError,
  onSubmit,
}: CodCsvImportDialogProps) {
  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!codImporting) onOpenChange(next);
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>Nhập tệp CSV đối soát COD từ hãng vận chuyển</DialogTitle>
          <DialogDescription>
            Tải lên bảng kê thu hộ COD (.csv UTF-8, tối đa 2 MiB và 5.000 dòng) để tự động khớp vận
            đơn và phát hiện sai lệch.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={onSubmit} className="space-y-4" noValidate>
          <FieldGroup className="gap-4">
            <Field className="gap-1.5">
              <FieldLabel htmlFor="cod-carrier">Hãng vận chuyển</FieldLabel>
              <FilterSelect
                id="cod-carrier"
                value={codCarrier}
                onValueChange={onCodCarrierChange}
                disabled={codImporting}
                options={carrierOptions}
                ariaLabel="Hãng vận chuyển"
              />
            </Field>

            <Field className="gap-1.5">
              <FieldLabel htmlFor="cod-date">Ngày đối soát</FieldLabel>
              <Input
                id="cod-date"
                type="date"
                value={codDate}
                disabled={codImporting}
                onChange={(e) => onCodDateChange(e.target.value)}
                className="bg-card tabular-nums"
              />
            </Field>

            <Field className="gap-1.5" data-invalid={Boolean(codError)}>
              <FieldLabel htmlFor="cod-file">Tệp CSV (.csv UTF-8)</FieldLabel>
              <Input
                id="cod-file"
                type="file"
                accept=".csv,text/csv"
                disabled={codImporting}
                aria-invalid={Boolean(codError)}
                onChange={(e) => onCodFileChange(e.target.files?.[0] ?? null)}
                className="h-auto bg-card py-2 text-[13px] file:mr-3 file:rounded-md file:border file:border-border file:bg-secondary file:px-2.5 file:py-1 file:text-xs file:font-medium file:text-secondary-foreground"
              />
              <FieldDescription>
                {codFile
                  ? `Đã chọn: ${codFile.name} (${Math.max(1, Math.round(codFile.size / 1024))} KB)`
                  : "Tối đa 2 MiB · 5.000 dòng. Cột bắt buộc: mã vận đơn và số tiền thu hộ."}
              </FieldDescription>
              <FieldError>{codError}</FieldError>
            </Field>
          </FieldGroup>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              disabled={codImporting}
              onClick={() => onOpenChange(false)}
            >
              Hủy
            </Button>
            <Button type="submit" disabled={codImporting}>
              <Upload className="size-4" aria-hidden="true" />
              <span>{codImporting ? "Đang đối soát..." : "Tải lên & đối soát COD"}</span>
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
