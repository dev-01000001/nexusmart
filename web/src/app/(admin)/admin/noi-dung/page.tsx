"use client";

import React, { Suspense, useMemo, useState } from "react";
import { computeCsrProgress } from "@/data";
import type { AdminEditorialSubmission, AdminBadgeRequest, AdminSearchRankingConfig } from "@/data";
import { useMockStore } from "@/mock/MockStoreContext";
import { Button } from "@/components";
import { formatDateVi, formatInt, formatPercent, formatVnd } from "@/lib/format";
import {
  BADGE_KIND_LABELS,
  BADGE_STATUS_META,
  EDITORIAL_STATUS_META,
  formatNowHHMM,
  formatShortTimeDateVi,
} from "@/features/admin/adminMeta";
import { updateAdminContentState, useAdminContentState } from "@/features/admin/auditStore";
import { AdminStateGate, AdminSuspenseFallback } from "@/features/admin/AdminStateGate";
import { StatusPill } from "@/features/admin/StatusPill";
import { ToggleSwitch } from "@/features/admin/ToggleSwitch";
import shared from "@/features/admin/adminShared.module.css";
import styles from "./page.module.css";

type ContentTab = "dieu_huong" | "tim_kiem" | "chien_dich";

function normalizeAccentless(raw: string): string {
  return raw
    .normalize("NFD")
    .replace(/[\u0300-\u036f]/g, "")
    .replace(/đ/g, "d")
    .replace(/Đ/g, "D")
    .toLowerCase()
    .trim();
}

const SAMPLE_MULTI_SHOP_SKUS = [
  {
    id: "sim-1",
    name: "Áo thun Cotton Compact 220gsm Trắng",
    shop: "Xưởng Dệt Bạch Đằng",
    relevance: 96,
    reviewFit: 94,
    sla: 98,
    sales30d: 88,
    trust: 95,
    inflatedListPrice: false,
  },
  {
    id: "sim-2",
    name: "Áo thun Dry-Fit Chạy bộ Cổ vịt",
    shop: "Kho Basic Sài Gòn",
    relevance: 92,
    reviewFit: 86,
    sla: 91,
    sales30d: 94,
    trust: 88,
    inflatedListPrice: false,
  },
  {
    id: "sim-3",
    name: "Áo thun cơ bản xả kho (giá gốc nâng +35%)",
    shop: "Kho Sỉ Phổ Thông",
    relevance: 90,
    reviewFit: 72,
    sla: 80,
    sales30d: 78,
    trust: 74,
    inflatedListPrice: true,
  },
];

function AdminContentWorkspace({ initialQuery }: { initialQuery: string }) {
  const content = useAdminContentState();
  const { showToast } = useMockStore();

  const initialTab: ContentTab = initialQuery.toUpperCase().startsWith("BV-")
    ? "chien_dich"
    : initialQuery.toUpperCase().startsWith("NH-")
      ? "chien_dich"
      : "dieu_huong";

  const [activeTab, setActiveTab] = useState<ContentTab>(initialTab);
  const [savedNotice, setSavedNotice] = useState<Record<string, string>>({});

  // Tab 1 drafts: announcement, new fabric, size template selection
  const [announceDraft, setAnnounceDraft] = useState(content.announcement);
  const [fabricCode, setFabricCode] = useState("");
  const [fabricName, setFabricName] = useState("");
  const [fabricComp, setFabricComp] = useState("");
  const [fabricGsm, setFabricGsm] = useState("");
  const [selectedSizeId, setSelectedSizeId] = useState(content.sizeTemplates[0]?.id ?? "sz-ao");
  const [sizeToleranceDraft, setSizeToleranceDraft] = useState<Record<string, string>>({});

  // Tab 2 drafts: trending keyword, synonym, ranking weights
  const [kwTerm, setKwTerm] = useState("");
  const [kwMode, setKwMode] = useState<"pin" | "block">("pin");
  const [synCanonical, setSynCanonical] = useState("");
  const [synList, setSynList] = useState("");
  const [rankingDraft, setRankingDraft] = useState<AdminSearchRankingConfig>(content.rankingConfig);
  const [rankingError, setRankingError] = useState("");

  // Tab 3 drafts: CSR new statement batch
  const [csrShop, setCsrShop] = useState("Xưởng Dệt Bạch Đằng");
  const [csrOrders, setCsrOrders] = useState(85);
  const [csrAmount, setCsrAmount] = useState(25500000);

  const markSaved = (key: string) => {
    setSavedNotice((prev) => ({ ...prev, [key]: `Đã lưu lúc ${formatNowHHMM()}` }));
  };

  const csrProgress = useMemo(() => computeCsrProgress(content.csrCampaign), [content.csrCampaign]);

  const pendingCampaignItems = useMemo(() => {
    const ed = content.editorialSubmissions.filter((x) => x.status === "cho_duyet").length;
    const bg = content.badgeRequests.filter((x) => x.status === "cho_duyet").length;
    return ed + bg;
  }, [content.editorialSubmissions, content.badgeRequests]);

  const activeSizeTemplate = useMemo(
    () => content.sizeTemplates.find((t) => t.id === selectedSizeId) ?? content.sizeTemplates[0],
    [content.sizeTemplates, selectedSizeId]
  );

  const rankingTotal =
    rankingDraft.relevanceWeight +
    rankingDraft.reviewFitWeight +
    rankingDraft.slaOnTimeWeight +
    rankingDraft.sales30dWeight +
    rankingDraft.shopTrustWeight;

  const simulatedRanks = useMemo(() => {
    const sum = Math.max(1, rankingTotal);
    return SAMPLE_MULTI_SHOP_SKUS.map((sku) => {
      const raw =
        (sku.relevance * rankingDraft.relevanceWeight +
          sku.reviewFit * rankingDraft.reviewFitWeight +
          sku.sla * rankingDraft.slaOnTimeWeight +
          sku.sales30d * rankingDraft.sales30dWeight +
          sku.trust * rankingDraft.shopTrustWeight) /
        sum;
      const penalty = sku.inflatedListPrice && rankingDraft.penalizeInflatedListPrice ? 25 : 0;
      return {
        ...sku,
        score: Math.max(0, Math.round((raw - penalty) * 10) / 10),
        penalized: penalty > 0,
      };
    }).sort((a, b) => b.score - a.score);
  }, [rankingDraft, rankingTotal]);

  // --- Handlers: Tab 1 ---
  const handleSaveAnnouncement = (e: React.FormEvent) => {
    e.preventDefault();
    updateAdminContentState((prev) => ({
      ...prev,
      announcement: { ...announceDraft },
    }));
    markSaved("announce");
    showToast("Đã lưu cấu hình thanh thông báo toàn site.");
  };

  const handleToggleMegaMenu = (id: string) => {
    const branch = content.megaMenu.find((b) => b.id === id);
    if (!branch) return;
    const nextActive = !branch.active;
    updateAdminContentState((prev) => ({
      ...prev,
      megaMenu: prev.megaMenu.map((b) => (b.id === id ? { ...b, active: nextActive } : b)),
    }));
  };

  const handleMoveMegaMenu = (idx: number, delta: -1 | 1) => {
    const targetIdx = idx + delta;
    if (targetIdx < 0 || targetIdx >= content.megaMenu.length) return;
    updateAdminContentState((prev) => {
      const next = [...prev.megaMenu];
      const [moved] = next.splice(idx, 1);
      next.splice(targetIdx, 0, moved);
      return {
        ...prev,
        megaMenu: next.map((b, i) => ({ ...b, priority: i + 1 })),
      };
    });
  };

  const handleToggleTaxonomy = (id: string) => {
    const node = content.taxonomy.find((t) => t.id === id);
    if (!node) return;
    const nextActive = !node.active;
    updateAdminContentState((prev) => ({
      ...prev,
      taxonomy: prev.taxonomy.map((t) => (t.id === id ? { ...t, active: nextActive } : t)),
    }));
  };

  const handleAddFabric = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanCode = fabricCode.trim().toUpperCase();
    const cleanName = fabricName.trim();
    if (!cleanCode || !cleanName) return;
    updateAdminContentState((prev) => ({
      ...prev,
      fabrics: [
        {
          id: `fab-${Date.now()}`,
          code: cleanCode,
          name: cleanName,
          composition: fabricComp.trim() || "100% Cotton sợi chuẩn",
          gsmRange: fabricGsm.trim() || "180–210 gsm",
          features: ["Chuẩn từ điển sàn"],
          careStandard: "Giặt nước mát 30°C, không tẩy",
          skuCount: 0,
          active: true,
        },
        ...prev.fabrics,
      ],
    }));
    setFabricCode("");
    setFabricName("");
    setFabricComp("");
    setFabricGsm("");
    showToast(`Đã thêm chất liệu ${cleanName} vào từ điển sàn.`);
  };

  const handleToggleAttrRule = (slug: string) => {
    const rule = content.attributeRules.find((r) => r.categorySlug === slug);
    if (!rule) return;
    const nextEnforce = !rule.enforceBeforePublish;
    updateAdminContentState((prev) => ({
      ...prev,
      attributeRules: prev.attributeRules.map((r) =>
        r.categorySlug === slug ? { ...r, enforceBeforePublish: nextEnforce } : r
      ),
    }));
  };

  const handleSaveSizeTemplate = () => {
    if (!activeSizeTemplate) return;
    const nextNote = (
      sizeToleranceDraft[activeSizeTemplate.id] ?? activeSizeTemplate.toleranceNote
    ).trim();
    updateAdminContentState((prev) => ({
      ...prev,
      sizeTemplates: prev.sizeTemplates.map((t) =>
        t.id === activeSizeTemplate.id ? { ...t, toleranceNote: nextNote || t.toleranceNote } : t
      ),
    }));
    markSaved("size");
    showToast("Đã cập nhật mẫu bảng size chuẩn sàn.");
  };

  // --- Handlers: Tab 2 ---
  const handleAddKeyword = (e: React.FormEvent) => {
    e.preventDefault();
    const term = kwTerm.trim();
    if (!term) return;
    const accentless = normalizeAccentless(term);
    const isBlock = kwMode === "block";
    updateAdminContentState((prev) => ({
      ...prev,
      trendingKeywords: [
        {
          id: `kw-${Date.now()}`,
          term,
          accentless,
          searches7d: 0,
          ctrPercent: 0,
          pinnedCampaign: !isBlock,
          status: isBlock ? "bi_chan" : "hien_thi",
          note: isBlock
            ? "Đã thêm vào danh sách chặn gợi ý tìm kiếm"
            : "Ghim chiến dịch bởi quản trị viên",
        },
        ...prev.trendingKeywords,
      ],
    }));
    setKwTerm("");
    showToast(isBlock ? `Đã chặn từ khóa "${term}".` : `Đã ghim từ khóa "${term}".`);
  };

  const handleToggleKeywordPin = (id: string) => {
    const kw = content.trendingKeywords.find((k) => k.id === id);
    if (!kw || kw.status === "bi_chan") return;
    const nextPin = !kw.pinnedCampaign;
    updateAdminContentState((prev) => ({
      ...prev,
      trendingKeywords: prev.trendingKeywords.map((k) =>
        k.id === id ? { ...k, pinnedCampaign: nextPin } : k
      ),
    }));
  };

  const handleToggleKeywordBlock = (id: string) => {
    const kw = content.trendingKeywords.find((k) => k.id === id);
    if (!kw) return;
    const nextStatus = kw.status === "bi_chan" ? "hien_thi" : "bi_chan";
    updateAdminContentState((prev) => ({
      ...prev,
      trendingKeywords: prev.trendingKeywords.map((k) =>
        k.id === id
          ? {
              ...k,
              status: nextStatus,
              pinnedCampaign: nextStatus === "bi_chan" ? false : k.pinnedCampaign,
            }
          : k
      ),
    }));
  };

  const handleAddSynonym = (e: React.FormEvent) => {
    e.preventDefault();
    const canonical = synCanonical.trim();
    const list = synList
      .split(",")
      .map((s) => s.trim())
      .filter(Boolean);
    if (!canonical || list.length === 0) return;
    updateAdminContentState((prev) => ({
      ...prev,
      synonyms: [
        {
          id: `syn-${Date.now()}`,
          canonical,
          accentless: normalizeAccentless(canonical),
          synonyms: list,
          active: true,
        },
        ...prev.synonyms,
      ],
    }));
    setSynCanonical("");
    setSynList("");
    showToast(`Đã thêm đồng nghĩa cho "${canonical}".`);
  };

  const handleSaveRanking = (e: React.FormEvent) => {
    e.preventDefault();
    if (rankingTotal !== 100) {
      setRankingError(`Tổng 5 trọng số phải đúng 100% (hiện tại ${rankingTotal}%).`);
      return;
    }
    setRankingError("");
    updateAdminContentState((prev) => ({
      ...prev,
      rankingConfig: { ...rankingDraft },
    }));
    markSaved("ranking");
    showToast("Đã cập nhật công thức xếp hạng đa shop.");
  };

  // --- Handlers: Tab 3 ---
  const handleMoveBlock = (idx: number, delta: -1 | 1) => {
    const targetIdx = idx + delta;
    if (targetIdx < 0 || targetIdx >= content.homepageBlocks.length) return;
    updateAdminContentState((prev) => {
      const next = [...prev.homepageBlocks];
      const [moved] = next.splice(idx, 1);
      next.splice(targetIdx, 0, moved);
      return {
        ...prev,
        homepageBlocks: next.map((b, i) => ({ ...b, order: i + 1 })),
      };
    });
  };

  const handleToggleBlock = (id: string) => {
    const blk = content.homepageBlocks.find((b) => b.id === id);
    if (!blk) return;
    const nextActive = !blk.active;
    updateAdminContentState((prev) => ({
      ...prev,
      homepageBlocks: prev.homepageBlocks.map((b) =>
        b.id === id ? { ...b, active: nextActive } : b
      ),
    }));
  };

  const handleToggleFabricTab = (id: string) => {
    const tab = content.csrCampaign.fabricTabs.find((t) => t.id === id);
    if (!tab) return;
    const nextPub = !tab.published;
    updateAdminContentState((prev) => ({
      ...prev,
      csrCampaign: {
        ...prev.csrCampaign,
        fabricTabs: prev.csrCampaign.fabricTabs.map((t) =>
          t.id === id ? { ...t, published: nextPub } : t
        ),
      },
    }));
  };

  const handleAddCsrStatement = (e: React.FormEvent) => {
    e.preventDefault();
    const cleanOrders = Math.max(1, Math.trunc(csrOrders));
    const cleanVnd = Math.max(100000, Math.trunc(csrAmount));
    const nextIndex = content.csrCampaign.statements.length + 1;
    const code = `SXN-2611-${nextIndex < 10 ? `0${nextIndex}` : String(nextIndex)}`;
    updateAdminContentState((prev) => ({
      ...prev,
      csrCampaign: {
        ...prev.csrCampaign,
        statements: [
          {
            id: `csr-${Date.now()}`,
            batchCode: code,
            date: "2026-11-20",
            shopName: csrShop,
            orderCount: cleanOrders,
            contributionVnd: cleanVnd,
            verified: true,
          },
          ...prev.csrCampaign.statements,
        ],
      },
    }));
    showToast(`Đã ghi nhận sao kê ${code} (${formatVnd(cleanVnd)}).`);
  };

  const handleEditorialDecision = (
    item: AdminEditorialSubmission,
    nextStatus: "da_dang" | "can_chinh_sua"
  ) => {
    updateAdminContentState((prev) => ({
      ...prev,
      editorialSubmissions: prev.editorialSubmissions.map((e) =>
        e.id === item.id ? { ...e, status: nextStatus } : e
      ),
    }));
    showToast(
      nextStatus === "da_dang"
        ? `Đã xuất bản bài "${item.title}".`
        : `Đã gửi yêu cầu sửa bài ${item.code}.`
    );
  };

  const handleBadgeDecision = (item: AdminBadgeRequest, nextStatus: "da_cap" | "tu_choi") => {
    updateAdminContentState((prev) => ({
      ...prev,
      badgeRequests: prev.badgeRequests.map((b) =>
        b.id === item.id ? { ...b, status: nextStatus } : b
      ),
    }));
    showToast(
      nextStatus === "da_cap"
        ? `Đã cấp nhãn ${BADGE_KIND_LABELS[item.badgeKind]} cho ${item.shopName}.`
        : `Đã từ chối hồ sơ nhãn ${item.code}.`
    );
  };

  const activeMegaCount = content.megaMenu.filter((m) => m.active).length;

  return (
    <div className={shared.page}>
      <header className={shared.titleRow}>
        <div className={shared.titleBlock}>
          <h1 className={shared.pageTitle}>Nội dung, danh mục & chiến dịch</h1>
          <p className={shared.pageSubtitle}>
            Bản xem trước, dữ liệu mẫu · {activeMegaCount}/{content.megaMenu.length} nhánh Mega-menu
            minh họa, {content.fabrics.length} chất liệu mẫu và {pendingCampaignItems} đề xuất mẫu.
          </p>
        </div>
      </header>

      <section className={shared.panel} role="note" style={{ marginTop: 16 }}>
        <StatusPill label="Bản xem trước, dữ liệu mẫu" tone="warning" />
        <p className={shared.pageSubtitle} style={{ marginTop: 8 }}>
          Phân hệ biên tập Mega-menu, xếp hạng tìm kiếm và chiến dịch CSR hiện sử dụng dữ liệu mẫu
          trên trình duyệt và chưa đồng bộ lên máy chủ.
        </p>
      </section>

      <div className={shared.tabsBar}>
        <div className={shared.tabList} role="tablist" aria-label="Phân mục quản trị nội dung">
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "dieu_huong"}
            className={`${shared.tabBtn} ${activeTab === "dieu_huong" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("dieu_huong")}
          >
            <span>Điều hướng & Danh mục</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "tim_kiem"}
            className={`${shared.tabBtn} ${activeTab === "tim_kiem" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("tim_kiem")}
          >
            <span>Tìm kiếm & Xu hướng</span>
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={activeTab === "chien_dich"}
            className={`${shared.tabBtn} ${activeTab === "chien_dich" ? shared.tabBtnActive : ""}`}
            onClick={() => setActiveTab("chien_dich")}
          >
            <span>Trang chủ & Chiến dịch</span>
            {pendingCampaignItems > 0 && (
              <span className={shared.tabCount}>{pendingCampaignItems}</span>
            )}
          </button>
        </div>
      </div>

      {/* TAB 1: ĐIỀU HƯỚNG & DANH MỤC */}
      {activeTab === "dieu_huong" && (
        <div className={styles.stack}>
          {/* 1A: Thanh thông báo toàn site */}
          <section className={shared.panel} aria-labelledby="nd-announce-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-announce-heading" className={shared.panelTitle}>
                Thanh thông báo toàn site
              </h2>
              <ToggleSwitch
                id="sw-site-announce"
                checked={announceDraft.enabled}
                label="Bật hoặc tắt thanh thông báo toàn site"
                onChange={(next) => setAnnounceDraft((prev) => ({ ...prev, enabled: next }))}
              />
            </div>

            <div className={styles.announcePreview}>
              <span>
                {announceDraft.enabled
                  ? announceDraft.message
                  : "Thanh thông báo hiện đang tạm ẩn trên cửa hàng người mua."}
              </span>
              {announceDraft.enabled && (
                <span className={styles.announcePreviewLink}>{announceDraft.linkLabel}</span>
              )}
            </div>

            <form onSubmit={handleSaveAnnouncement}>
              <div className={styles.formGrid}>
                <div className={`${styles.fieldGroup} ${styles.fieldSpanTwo}`}>
                  <label htmlFor="nd-ann-msg" className={shared.fieldLabel}>
                    Nội dung hiển thị trên thanh đầu trang
                  </label>
                  <input
                    id="nd-ann-msg"
                    type="text"
                    className={shared.textInput}
                    value={announceDraft.message}
                    onChange={(e) =>
                      setAnnounceDraft((prev) => ({ ...prev, message: e.target.value }))
                    }
                  />
                </div>
                <div className={styles.fieldGroup}>
                  <label htmlFor="nd-ann-cta" className={shared.fieldLabel}>
                    Nhãn liên kết
                  </label>
                  <input
                    id="nd-ann-cta"
                    type="text"
                    className={shared.textInput}
                    value={announceDraft.linkLabel}
                    onChange={(e) =>
                      setAnnounceDraft((prev) => ({ ...prev, linkLabel: e.target.value }))
                    }
                  />
                </div>
              </div>
              <div className={styles.formFooter}>
                <span className={shared.savedStamp}>
                  {savedNotice.announce ?? announceDraft.scheduleLabel}
                </span>
                <Button type="submit" variant="primary" size="sm" shape="app">
                  Lưu thanh thông báo
                </Button>
              </div>
            </form>
          </section>

          {/* 1B: Mega-menu 3 cấp */}
          <section className={shared.panel} aria-labelledby="nd-megamenu-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-megamenu-heading" className={shared.panelTitle}>
                Cây Mega-menu 3 cấp theo nhu cầu
              </h2>
              <span className={shared.panelMeta}>
                {activeMegaCount}/{content.megaMenu.length} nhánh hiển thị
              </span>
            </div>

            <div className={styles.megaList}>
              {content.megaMenu.map((branch, idx) => (
                <div key={branch.id} className={styles.megaRow}>
                  <div className={styles.megaHeader}>
                    <div className={styles.megaTitleGroup}>
                      <span className={shared.cellPrimary}>{branch.label}</span>
                      <span className={shared.cellSub}>
                        /{branch.slug} · Slot banner: {branch.bannerSlotTitle}
                      </span>
                      <StatusPill
                        label={branch.active ? "Đang bật" : "Đang ẩn"}
                        tone={branch.active ? "positive" : "neutral"}
                      />
                    </div>
                    <div className={styles.megaActions}>
                      <button
                        type="button"
                        className={shared.rowActionBtn}
                        disabled={idx === 0}
                        onClick={() => handleMoveMegaMenu(idx, -1)}
                      >
                        Lên
                      </button>
                      <button
                        type="button"
                        className={shared.rowActionBtn}
                        disabled={idx === content.megaMenu.length - 1}
                        onClick={() => handleMoveMegaMenu(idx, 1)}
                      >
                        Xuống
                      </button>
                      <ToggleSwitch
                        id={`sw-mm-${branch.id}`}
                        checked={branch.active}
                        label={`Bật hoặc ẩn nhánh ${branch.label}`}
                        onChange={() => handleToggleMegaMenu(branch.id)}
                      />
                    </div>
                  </div>

                  <div className={styles.megaColumns}>
                    {branch.columns.map((col) => (
                      <div key={col.id}>
                        <div className={styles.megaColTitle}>{col.title}</div>
                        <div className={styles.megaColItems}>{col.items.join(" · ")}</div>
                      </div>
                    ))}
                  </div>
                </div>
              ))}
            </div>
          </section>

          {/* 1C: Taxonomy 3 cấp */}
          <section className={shared.panel} aria-labelledby="nd-taxonomy-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-taxonomy-heading" className={shared.panelTitle}>
                Chuẩn hóa Taxonomy 3 cấp toàn sàn
              </h2>
              <span className={shared.panelMeta}>{content.taxonomy.length} nhánh ngành hàng</span>
            </div>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Mã & cấp 1 / cấp 2</th>
                    <th>Dòng sản phẩm cấp 3</th>
                    <th className={shared.colNum}>SKU gắn kết</th>
                    <th className={shared.colNum}>Thuộc tính bắt buộc</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {content.taxonomy.map((node) => (
                    <tr key={node.id}>
                      <td>
                        <div className={shared.cellPrimary}>
                          {node.level1} / {node.level2}
                        </div>
                        <div className={shared.cellSub}>{node.code}</div>
                      </td>
                      <td>{node.level3}</td>
                      <td className={shared.colNum}>{formatInt(node.skuCount)}</td>
                      <td className={shared.colNum}>{formatInt(node.requiredAttrCount)} trường</td>
                      <td>
                        <StatusPill
                          label={node.active ? "Đang dùng" : "Tạm ẩn"}
                          tone={node.active ? "positive" : "neutral"}
                        />
                      </td>
                      <td className={shared.colAction}>
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={() => handleToggleTaxonomy(node.id)}
                        >
                          {node.active ? "Tạm ẩn" : "Kích hoạt"}
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {content.taxonomy.map((node) => (
                <div key={node.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>
                      {node.level1} / {node.level2}
                    </span>
                    <span className={shared.mobileMetric}>{formatInt(node.skuCount)} SKU</span>
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>{node.level3}</span>
                    <StatusPill
                      label={node.active ? "Đang dùng" : "Tạm ẩn"}
                      tone={node.active ? "positive" : "neutral"}
                    />
                  </div>
                </div>
              ))}
            </div>
          </section>

          {/* 1D: Từ điển chất liệu & thuộc tính bắt buộc */}
          <section className={shared.panel} aria-labelledby="nd-fabric-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-fabric-heading" className={shared.panelTitle}>
                Từ điển chất liệu & thuộc tính bắt buộc theo ngành
              </h2>
              <span className={shared.panelMeta}>{content.fabrics.length} chất liệu chuẩn</span>
            </div>

            <form className={styles.inlineAddForm} onSubmit={handleAddFabric}>
              <div className={styles.fieldGroup}>
                <label htmlFor="fab-code" className={shared.fieldLabel}>
                  Mã chất liệu
                </label>
                <input
                  id="fab-code"
                  type="text"
                  className={shared.textInput}
                  value={fabricCode}
                  onChange={(e) => setFabricCode(e.target.value)}
                  placeholder="VD: TER-320"
                />
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="fab-name" className={shared.fieldLabel}>
                  Tên chất liệu
                </label>
                <input
                  id="fab-name"
                  type="text"
                  className={shared.textInput}
                  value={fabricName}
                  onChange={(e) => setFabricName(e.target.value)}
                  placeholder="VD: French Terry 320gsm"
                />
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="fab-gsm" className={shared.fieldLabel}>
                  Thành phần / GSM
                </label>
                <input
                  id="fab-gsm"
                  type="text"
                  className={shared.textInput}
                  value={fabricGsm}
                  onChange={(e) => {
                    setFabricGsm(e.target.value);
                    setFabricComp(e.target.value);
                  }}
                  placeholder="VD: 300–330 gsm"
                />
              </div>
              <Button type="submit" variant="ink" size="sm" shape="app">
                Thêm chất liệu
              </Button>
            </form>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Chất liệu & thành phần</th>
                    <th>Tính năng vải & chuẩn bảo quản</th>
                    <th className={shared.colNum}>Định lượng</th>
                    <th className={shared.colNum}>SKU dùng</th>
                    <th>Trạng thái</th>
                  </tr>
                </thead>
                <tbody>
                  {content.fabrics.map((fab) => (
                    <tr key={fab.id}>
                      <td>
                        <div className={shared.cellPrimary}>
                          {fab.name} ({fab.code})
                        </div>
                        <div className={shared.cellSub}>{fab.composition}</div>
                      </td>
                      <td>
                        <div>{fab.features.join(" · ")}</div>
                        <div className={shared.cellSub}>{fab.careStandard}</div>
                      </td>
                      <td className={shared.colNum}>{fab.gsmRange}</td>
                      <td className={shared.colNum}>{formatInt(fab.skuCount)}</td>
                      <td>
                        <StatusPill
                          label={fab.active ? "Đang áp dụng" : "Ngưng dùng"}
                          tone={fab.active ? "positive" : "neutral"}
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {content.fabrics.map((fab) => (
                <div key={fab.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>{fab.name}</span>
                    <span className={shared.mobileMetric}>{fab.gsmRange}</span>
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>{fab.features.join(" · ")}</span>
                    <StatusPill label={fab.code} tone="neutral" />
                  </div>
                </div>
              ))}
            </div>

            <div className={styles.switchList}>
              {content.attributeRules.map((rule) => (
                <div key={rule.categorySlug} className={styles.switchRow}>
                  <div className={styles.switchCopy}>
                    <span className={styles.switchTitle}>
                      Thuộc tính bắt buộc ngành {rule.categoryName}
                    </span>
                    <span className={styles.switchDesc}>{rule.requiredFields.join(" · ")}</span>
                  </div>
                  <ToggleSwitch
                    id={`sw-attr-${rule.categorySlug}`}
                    checked={rule.enforceBeforePublish}
                    label={`Bắt buộc thuộc tính trước khi đăng bán cho ngành ${rule.categoryName}`}
                    onChange={() => handleToggleAttrRule(rule.categorySlug)}
                  />
                </div>
              ))}
            </div>
          </section>

          {/* 1E: Mẫu bảng size chuẩn sàn */}
          {activeSizeTemplate && (
            <section className={shared.panel} aria-labelledby="nd-size-heading">
              <div className={shared.panelHeader}>
                <h2 id="nd-size-heading" className={shared.panelTitle}>
                  Mẫu bảng size chuẩn sàn cho gian hàng
                </h2>
                <div className={styles.subSwitchRow}>
                  {content.sizeTemplates.map((tpl) => (
                    <button
                      key={tpl.id}
                      type="button"
                      className={`${styles.subSwitchBtn} ${
                        tpl.id === activeSizeTemplate.id ? styles.subSwitchBtnActive : ""
                      }`}
                      onClick={() => setSelectedSizeId(tpl.id)}
                    >
                      {tpl.groupName}
                    </button>
                  ))}
                </div>
              </div>

              <div className={shared.tableWrap}>
                <table className={shared.dataTable}>
                  <thead>
                    <tr>
                      <th>Cỡ chuẩn</th>
                      <th>Chiều cao tham chiếu</th>
                      <th>Cân nặng tham chiếu</th>
                      <th className={shared.colNum}>Thông số chính</th>
                    </tr>
                  </thead>
                  <tbody>
                    {activeSizeTemplate.rows.map((r) => (
                      <tr key={r.size}>
                        <td className={shared.cellPrimary}>{r.size}</td>
                        <td>{r.heightRange}</td>
                        <td>{r.weightRange}</td>
                        <td className={shared.colNum}>{r.measurement}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <div className={shared.mobileList}>
                {activeSizeTemplate.rows.map((r) => (
                  <div key={r.size} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>Cỡ {r.size}</span>
                      <span className={shared.mobileMetric}>{r.measurement}</span>
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {r.heightRange} · {r.weightRange}
                      </span>
                    </div>
                  </div>
                ))}
              </div>

              <div className={styles.formFooter}>
                <div className={shared.detailNoteGroup}>
                  <label htmlFor="sz-tol" className={shared.fieldLabel}>
                    Ghi chú dung sai & quy chuẩn đổi size ({activeSizeTemplate.appliesTo})
                  </label>
                  <input
                    id="sz-tol"
                    type="text"
                    className={shared.textInput}
                    value={
                      sizeToleranceDraft[activeSizeTemplate.id] ?? activeSizeTemplate.toleranceNote
                    }
                    onChange={(e) =>
                      setSizeToleranceDraft((prev) => ({
                        ...prev,
                        [activeSizeTemplate.id]: e.target.value,
                      }))
                    }
                  />
                </div>
                <Button variant="ink" size="sm" shape="app" onClick={handleSaveSizeTemplate}>
                  Lưu mẫu bảng size
                </Button>
              </div>
              {savedNotice.size && <p className={shared.savedStamp}>{savedNotice.size}</p>}
            </section>
          )}
        </div>
      )}

      {/* TAB 2: TÌM KIẾM & XU HƯỚNG */}
      {activeTab === "tim_kiem" && (
        <div className={styles.stack}>
          {/* 2A: Từ khóa xu hướng & lọc vi phạm */}
          <section className={shared.panel} aria-labelledby="nd-trending-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-trending-heading" className={shared.panelTitle}>
                Từ khóa xu hướng & lọc cụm từ vi phạm
              </h2>
              <span className={shared.panelMeta}>
                {content.trendingKeywords.filter((k) => k.pinnedCampaign).length} từ khóa ghim chiến
                dịch
              </span>
            </div>

            <form className={styles.inlineAddFormThree} onSubmit={handleAddKeyword}>
              <div className={styles.fieldGroup}>
                <label htmlFor="kw-term" className={shared.fieldLabel}>
                  Từ khóa mới
                </label>
                <input
                  id="kw-term"
                  type="text"
                  className={shared.textInput}
                  value={kwTerm}
                  onChange={(e) => setKwTerm(e.target.value)}
                  placeholder="Nhập từ khóa chiến dịch hoặc cụm vi phạm..."
                />
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="kw-mode" className={shared.fieldLabel}>
                  Chế độ xử lý
                </label>
                <select
                  id="kw-mode"
                  className={shared.toolbarSelect}
                  value={kwMode}
                  onChange={(e) => setKwMode(e.target.value as "pin" | "block")}
                >
                  <option value="pin">Ghim chiến dịch xu hướng</option>
                  <option value="block">Chặn từ khóa vi phạm</option>
                </select>
              </div>
              <Button type="submit" variant="primary" size="sm" shape="app">
                Áp dụng từ khóa
              </Button>
            </form>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Từ khóa & chỉ mục bỏ dấu</th>
                    <th className={shared.colNum}>Lượt tìm 7 ngày</th>
                    <th className={shared.colNum}>CTR</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {content.trendingKeywords.map((kw) => (
                    <tr key={kw.id}>
                      <td>
                        <div className={shared.cellPrimary}>{kw.term}</div>
                        <div className={shared.cellSub}>
                          {kw.accentless} · {kw.note}
                        </div>
                      </td>
                      <td className={shared.colNum}>{formatInt(kw.searches7d)}</td>
                      <td className={shared.colNum}>{formatPercent(kw.ctrPercent, 1, false)}</td>
                      <td>
                        <StatusPill
                          label={
                            kw.status === "bi_chan"
                              ? "Bị chặn"
                              : kw.pinnedCampaign
                                ? "Ghim chiến dịch"
                                : "Tự động"
                          }
                          tone={
                            kw.status === "bi_chan"
                              ? "negative"
                              : kw.pinnedCampaign
                                ? "positive"
                                : "neutral"
                          }
                        />
                      </td>
                      <td className={shared.colAction}>
                        <div className={styles.actionInline}>
                          {kw.status !== "bi_chan" && (
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              onClick={() => handleToggleKeywordPin(kw.id)}
                            >
                              {kw.pinnedCampaign ? "Bỏ ghim" : "Ghim"}
                            </button>
                          )}
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            onClick={() => handleToggleKeywordBlock(kw.id)}
                          >
                            {kw.status === "bi_chan" ? "Khôi phục" : "Chặn"}
                          </button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {content.trendingKeywords.map((kw) => (
                <div key={kw.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>{kw.term}</span>
                    <span className={shared.mobileMetric}>{formatInt(kw.searches7d)} lượt</span>
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>{kw.accentless}</span>
                    <StatusPill
                      label={
                        kw.status === "bi_chan"
                          ? "Bị chặn"
                          : kw.pinnedCampaign
                            ? "Ghim chiến dịch"
                            : "Tự động"
                      }
                      tone={
                        kw.status === "bi_chan"
                          ? "negative"
                          : kw.pinnedCampaign
                            ? "positive"
                            : "neutral"
                      }
                    />
                  </div>
                </div>
              ))}
            </div>
          </section>

          {/* 2B: Đồng nghĩa & bỏ dấu */}
          <section className={shared.panel} aria-labelledby="nd-syn-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-syn-heading" className={shared.panelTitle}>
                Từ điển đồng nghĩa & tìm kiếm không dấu
              </h2>
              <span className={shared.panelMeta}>{content.synonyms.length} nhóm ánh xạ</span>
            </div>

            <form className={styles.inlineAddFormThree} onSubmit={handleAddSynonym}>
              <div className={styles.fieldGroup}>
                <label htmlFor="syn-root" className={shared.fieldLabel}>
                  Cụm chuẩn tiếng Việt
                </label>
                <input
                  id="syn-root"
                  type="text"
                  className={shared.textInput}
                  value={synCanonical}
                  onChange={(e) => setSynCanonical(e.target.value)}
                  placeholder="VD: áo khoác gió"
                />
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="syn-items" className={shared.fieldLabel}>
                  Từ đồng nghĩa (cách bằng dấu phẩy)
                </label>
                <input
                  id="syn-items"
                  type="text"
                  className={shared.textInput}
                  value={synList}
                  onChange={(e) => setSynList(e.target.value)}
                  placeholder="VD: ao gio, windbreaker, ao chong gio"
                />
              </div>
              <Button type="submit" variant="ink" size="sm" shape="app">
                Thêm đồng nghĩa
              </Button>
            </form>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Cụm chuẩn & bản bỏ dấu</th>
                    <th>Các biến thể đồng nghĩa được khớp</th>
                    <th>Trạng thái</th>
                  </tr>
                </thead>
                <tbody>
                  {content.synonyms.map((syn) => (
                    <tr key={syn.id}>
                      <td>
                        <div className={shared.cellPrimary}>{syn.canonical}</div>
                        <div className={shared.cellSub}>{syn.accentless}</div>
                      </td>
                      <td>{syn.synonyms.join(", ")}</td>
                      <td>
                        <StatusPill
                          label={syn.active ? "Đang đồng bộ" : "Tạm tắt"}
                          tone={syn.active ? "positive" : "neutral"}
                        />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {content.synonyms.map((syn) => (
                <div key={syn.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>{syn.canonical}</span>
                    <StatusPill label={syn.active ? "Đồng bộ" : "Tắt"} tone="positive" />
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>{syn.synonyms.join(", ")}</span>
                  </div>
                </div>
              ))}
            </div>
          </section>

          {/* 2C: Công thức trọng số xếp hạng đa shop chống spam giá ảo */}
          <section className={shared.panel} aria-labelledby="nd-rank-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-rank-heading" className={shared.panelTitle}>
                Công thức xếp hạng đa gian hàng & chống spam giá ảo
              </h2>
              <span
                className={`${shared.panelMeta} ${rankingTotal !== 100 ? shared.numNegative : ""}`}
              >
                Tổng trọng số: {rankingTotal}% / 100%
              </span>
            </div>

            <div className={styles.splitGrid}>
              <form onSubmit={handleSaveRanking}>
                <div className={styles.weightRow}>
                  <label htmlFor="rk-rel" className={styles.weightLabel}>
                    Độ khớp từ khóa & thuộc tính chuẩn (%)
                  </label>
                  <input
                    id="rk-rel"
                    type="number"
                    min={0}
                    max={100}
                    className={styles.weightInput}
                    value={rankingDraft.relevanceWeight}
                    onChange={(e) =>
                      setRankingDraft((p) => ({
                        ...p,
                        relevanceWeight: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                      }))
                    }
                  />
                </div>
                <div className={styles.weightRow}>
                  <label htmlFor="rk-rev" className={styles.weightLabel}>
                    Điểm đánh giá thật & độ vừa size (%)
                  </label>
                  <input
                    id="rk-rev"
                    type="number"
                    min={0}
                    max={100}
                    className={styles.weightInput}
                    value={rankingDraft.reviewFitWeight}
                    onChange={(e) =>
                      setRankingDraft((p) => ({
                        ...p,
                        reviewFitWeight: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                      }))
                    }
                  />
                </div>
                <div className={styles.weightRow}>
                  <label htmlFor="rk-sla" className={styles.weightLabel}>
                    Tỷ lệ giao đúng hẹn SLA kho (%)
                  </label>
                  <input
                    id="rk-sla"
                    type="number"
                    min={0}
                    max={100}
                    className={styles.weightInput}
                    value={rankingDraft.slaOnTimeWeight}
                    onChange={(e) =>
                      setRankingDraft((p) => ({
                        ...p,
                        slaOnTimeWeight: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                      }))
                    }
                  />
                </div>
                <div className={styles.weightRow}>
                  <label htmlFor="rk-sales" className={styles.weightLabel}>
                    Lượt bán thực tế 30 ngày (%)
                  </label>
                  <input
                    id="rk-sales"
                    type="number"
                    min={0}
                    max={100}
                    className={styles.weightInput}
                    value={rankingDraft.sales30dWeight}
                    onChange={(e) =>
                      setRankingDraft((p) => ({
                        ...p,
                        sales30dWeight: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                      }))
                    }
                  />
                </div>
                <div className={styles.weightRow}>
                  <label htmlFor="rk-trust" className={styles.weightLabel}>
                    Uy tín gian hàng & tỷ lệ hoàn thấp (%)
                  </label>
                  <input
                    id="rk-trust"
                    type="number"
                    min={0}
                    max={100}
                    className={styles.weightInput}
                    value={rankingDraft.shopTrustWeight}
                    onChange={(e) =>
                      setRankingDraft((p) => ({
                        ...p,
                        shopTrustWeight: Math.max(0, Math.trunc(Number(e.target.value) || 0)),
                      }))
                    }
                  />
                </div>

                <div className={styles.switchList}>
                  <div className={styles.switchRow}>
                    <div className={styles.switchCopy}>
                      <span className={styles.switchTitle}>
                        Phạt hạng SKU nâng giá gốc ảo trước khuyến mãi
                      </span>
                      <span className={styles.switchDesc}>
                        Trừ 25 điểm xếp hạng nếu giá gốc tăng hơn 30% so với trung vị 14 ngày.
                      </span>
                    </div>
                    <ToggleSwitch
                      id="sw-rk-fake"
                      checked={rankingDraft.penalizeInflatedListPrice}
                      label="Bật phạt hạng nâng giá gốc ảo"
                      onChange={(next) =>
                        setRankingDraft((p) => ({ ...p, penalizeInflatedListPrice: next }))
                      }
                    />
                  </div>
                  <div className={styles.switchRow}>
                    <div className={styles.switchCopy}>
                      <span className={styles.switchTitle}>
                        Ẩn biến thể mồi giá thấp bất thường
                      </span>
                      <span className={styles.switchDesc}>
                        Chặn tách phụ kiện lẻ giá dưới 35% giá trung vị để kéo hiển thị giá rẻ.
                      </span>
                    </div>
                    <ToggleSwitch
                      id="sw-rk-bait"
                      checked={rankingDraft.hideBaitSkuOutliers}
                      label="Bật ẩn biến thể mồi giá thấp bất thường"
                      onChange={(next) =>
                        setRankingDraft((p) => ({ ...p, hideBaitSkuOutliers: next }))
                      }
                    />
                  </div>
                  <div className={styles.switchRow}>
                    <div className={styles.switchCopy}>
                      <span className={styles.switchTitle}>
                        Ưu tiên SKU còn đủ dải size phổ biến (M–XL)
                      </span>
                      <span className={styles.switchDesc}>
                        Đẩy nhẹ sản phẩm còn hàng các cỡ bán chạy để giảm tỷ lệ thoát trang.
                      </span>
                    </div>
                    <ToggleSwitch
                      id="sw-rk-size"
                      checked={rankingDraft.boostFullSizeAvailability}
                      label="Ưu tiên sản phẩm đủ size phổ biến"
                      onChange={(next) =>
                        setRankingDraft((p) => ({ ...p, boostFullSizeAvailability: next }))
                      }
                    />
                  </div>
                </div>

                {rankingError && <p className={shared.validationError}>{rankingError}</p>}

                <div className={styles.formFooter}>
                  <span className={shared.savedStamp}>
                    {savedNotice.ranking ?? "Áp dụng cho truy vấn danh mục và kết quả tìm kiếm."}
                  </span>
                  <Button type="submit" variant="ink" size="sm" shape="app">
                    Lưu công thức xếp hạng
                  </Button>
                </div>
              </form>

              <div className={styles.simWell}>
                <h3 className={shared.panelTitle}>
                  Mô phỏng thứ hạng truy vấn &ldquo;ao thun&rdquo;
                </h3>
                {simulatedRanks.map((sku, idx) => (
                  <div key={sku.id} className={styles.simItem}>
                    <div>
                      <div className={shared.cellPrimary}>
                        {idx + 1}. {sku.name}
                      </div>
                      <div className={shared.cellSub}>
                        {sku.shop}
                        {sku.penalized ? " · Bị trừ 25đ do nâng giá gốc ảo" : ""}
                      </div>
                    </div>
                    <span className={styles.simScore}>{sku.score} đ</span>
                  </div>
                ))}
              </div>
            </div>
          </section>
        </div>
      )}

      {/* TAB 3: TRANG CHỦ & CHIẾN DỊCH */}
      {activeTab === "chien_dich" && (
        <div className={styles.stack}>
          {/* 3A: CMS sắp xếp khối trang chủ */}
          <section className={shared.panel} aria-labelledby="nd-home-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-home-heading" className={shared.panelTitle}>
                CMS sắp xếp các khối trang chủ
              </h2>
              <span className={shared.panelMeta}>
                {content.homepageBlocks.filter((b) => b.active).length}/
                {content.homepageBlocks.length} khối đang hiển thị
              </span>
            </div>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Khối hiển thị & luật chọn đa shop</th>
                    <th>Loại slot</th>
                    <th className={shared.colNum}>Số mục</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thứ tự</th>
                  </tr>
                </thead>
                <tbody>
                  {content.homepageBlocks.map((blk, idx) => (
                    <tr key={blk.id}>
                      <td>
                        <div className={shared.cellPrimary}>
                          {blk.order}. {blk.title}
                        </div>
                        <div className={shared.cellSub}>
                          {blk.code} · {blk.selectionRule}
                        </div>
                      </td>
                      <td>{blk.slotType}</td>
                      <td className={shared.colNum}>{formatInt(blk.maxItems)}</td>
                      <td>
                        <StatusPill
                          label={blk.active ? "Hiển thị" : "Tạm ẩn"}
                          tone={blk.active ? "positive" : "neutral"}
                        />
                      </td>
                      <td className={shared.colAction}>
                        <div className={styles.actionInline}>
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            disabled={idx === 0}
                            onClick={() => handleMoveBlock(idx, -1)}
                          >
                            Lên
                          </button>
                          <button
                            type="button"
                            className={shared.rowActionBtn}
                            disabled={idx === content.homepageBlocks.length - 1}
                            onClick={() => handleMoveBlock(idx, 1)}
                          >
                            Xuống
                          </button>
                          <ToggleSwitch
                            id={`sw-blk-${blk.id}`}
                            checked={blk.active}
                            label={`Bật hoặc ẩn khối ${blk.title}`}
                            onChange={() => handleToggleBlock(blk.id)}
                          />
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {content.homepageBlocks.map((blk) => (
                <div key={blk.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>
                      {blk.order}. {blk.title}
                    </span>
                    <ToggleSwitch
                      id={`sw-blk-m-${blk.id}`}
                      checked={blk.active}
                      label={`Bật hoặc ẩn khối ${blk.title}`}
                      onChange={() => handleToggleBlock(blk.id)}
                    />
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>{blk.selectionRule}</span>
                  </div>
                </div>
              ))}
            </div>
          </section>

          {/* 3B: Trang chiến dịch công nghệ vải & CSR Sợi Xanh Nexus */}
          <section className={shared.panel} aria-labelledby="nd-csr-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-csr-heading" className={shared.panelTitle}>
                Chiến dịch công nghệ vải & CSR {content.csrCampaign.title}
              </h2>
              <span className={shared.panelMeta}>{content.csrCampaign.campaignCode}</span>
            </div>

            <div className={styles.csrProgressBox}>
              <div className={styles.csrMetaRow}>
                <span className={styles.csrAmountLead}>{formatVnd(csrProgress.totalVnd)}</span>
                <span className={styles.csrTargetLabel}>
                  Đạt {formatPercent(csrProgress.percent, 1, false)} mục tiêu{" "}
                  {formatVnd(content.csrCampaign.targetVnd)} · {formatInt(csrProgress.totalOrders)}{" "}
                  đơn hàng
                </span>
              </div>
              <div
                className={shared.progressTrack}
                role="progressbar"
                aria-valuenow={Math.round(csrProgress.percent)}
                aria-valuemin={0}
                aria-valuemax={100}
                aria-label="Tiến độ đóng góp chiến dịch Sợi Xanh Nexus"
              >
                <div
                  className={shared.progressFill}
                  style={{ width: `${Math.min(100, csrProgress.percent)}%` }}
                />
              </div>
              <p className={shared.cellSub}>{content.csrCampaign.summary}</p>
            </div>

            <div className={styles.fabricTabGrid}>
              {content.csrCampaign.fabricTabs.map((tab) => (
                <div key={tab.id} className={styles.fabricTabCard}>
                  <div>
                    <div className={shared.cellPrimary}>{tab.title}</div>
                    <div className={shared.cellSub}>
                      {tab.fabricCode} · {formatInt(tab.skuCount)} SKU tham gia
                    </div>
                  </div>
                  <ToggleSwitch
                    id={`sw-ft-${tab.id}`}
                    checked={tab.published}
                    label={`Bật hoặc ẩn tab công nghệ vải ${tab.title}`}
                    onChange={() => handleToggleFabricTab(tab.id)}
                  />
                </div>
              ))}
            </div>

            <form className={styles.inlineAddForm} onSubmit={handleAddCsrStatement}>
              <div className={styles.fieldGroup}>
                <label htmlFor="csr-shop" className={shared.fieldLabel}>
                  Gian hàng đối tác
                </label>
                <select
                  id="csr-shop"
                  className={shared.toolbarSelect}
                  value={csrShop}
                  onChange={(e) => setCsrShop(e.target.value)}
                >
                  <option value="Xưởng Dệt Bạch Đằng">Xưởng Dệt Bạch Đằng</option>
                  <option value="Sợi Tự Nhiên Đà Nẵng">Sợi Tự Nhiên Đà Nẵng</option>
                  <option value="Dệt Kim Hà Nội">Dệt Kim Hà Nội</option>
                  <option value="Kho Basic Sài Gòn">Kho Basic Sài Gòn</option>
                </select>
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="csr-ord" className={shared.fieldLabel}>
                  Số đơn hợp lệ
                </label>
                <input
                  id="csr-ord"
                  type="number"
                  min={1}
                  className={shared.textInput}
                  value={csrOrders}
                  onChange={(e) => setCsrOrders(Number(e.target.value) || 0)}
                />
              </div>
              <div className={styles.fieldGroup}>
                <label htmlFor="csr-vnd" className={shared.fieldLabel}>
                  Số tiền trích quỹ (VND)
                </label>
                <input
                  id="csr-vnd"
                  type="number"
                  min={100000}
                  step={500000}
                  className={shared.textInput}
                  value={csrAmount}
                  onChange={(e) => setCsrAmount(Number(e.target.value) || 0)}
                />
              </div>
              <Button type="submit" variant="primary" size="sm" shape="app">
                Ghi sao kê mẫu
              </Button>
            </form>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Mã sao kê & gian hàng</th>
                    <th>Ngày chốt</th>
                    <th className={shared.colNum}>Đơn hợp lệ</th>
                    <th className={shared.colNum}>Đóng góp (10%)</th>
                    <th>Đối chiếu</th>
                  </tr>
                </thead>
                <tbody>
                  {content.csrCampaign.statements.map((st) => (
                    <tr key={st.id}>
                      <td>
                        <div className={shared.cellPrimary}>{st.batchCode}</div>
                        <div className={shared.cellSub}>{st.shopName}</div>
                      </td>
                      <td>{formatDateVi(st.date)}</td>
                      <td className={shared.colNum}>{formatInt(st.orderCount)}</td>
                      <td className={shared.colNum}>{formatVnd(st.contributionVnd)}</td>
                      <td>
                        <StatusPill label="Đã đối chiếu" tone="positive" />
                      </td>
                    </tr>
                  ))}
                </tbody>
                <tfoot>
                  <tr className={styles.tfootRow}>
                    <td colSpan={2}>Tổng sao kê Sợi Xanh Nexus</td>
                    <td className={shared.colNum}>{formatInt(csrProgress.totalOrders)}</td>
                    <td className={shared.colNum}>{formatVnd(csrProgress.totalVnd)}</td>
                    <td />
                  </tr>
                </tfoot>
              </table>
            </div>

            <div className={shared.mobileList}>
              {content.csrCampaign.statements.map((st) => (
                <div key={st.id} className={shared.mobileItem}>
                  <div className={shared.mobileLine1}>
                    <span className={shared.mobileIdentity}>{st.shopName}</span>
                    <span className={shared.mobileMetric}>{formatVnd(st.contributionVnd)}</span>
                  </div>
                  <div className={shared.mobileLine2}>
                    <span className={shared.mobileMetaText}>
                      {st.batchCode} · {formatDateVi(st.date)} · {formatInt(st.orderCount)} đơn
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </section>

          {/* 3C: Duyệt bài Sổ tay vải & Phối đồ (Blog / Lookbook từ Seller) */}
          <section className={shared.panel} aria-labelledby="nd-blog-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-blog-heading" className={shared.panelTitle}>
                Duyệt bài Sổ tay vải & Phối đồ từ gian hàng
              </h2>
              <span className={shared.panelMeta}>
                {content.editorialSubmissions.filter((x) => x.status === "cho_duyet").length} chờ
                duyệt
              </span>
            </div>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Tiêu đề bài viết & gian hàng</th>
                    <th>Chuyên mục</th>
                    <th className={shared.colNum}>SKU nhúng</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {content.editorialSubmissions.map((ed) => {
                    const meta = EDITORIAL_STATUS_META[ed.status];
                    return (
                      <tr key={ed.id}>
                        <td>
                          <div className={shared.cellPrimary}>{ed.title}</div>
                          <div className={shared.cellSub}>
                            {ed.code} · {ed.shopName} · {formatShortTimeDateVi(ed.submittedAt)}
                          </div>
                        </td>
                        <td>{ed.category}</td>
                        <td className={shared.colNum}>{formatInt(ed.embeddedSkuCount)} SKU</td>
                        <td>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </td>
                        <td className={shared.colAction}>
                          <div className={styles.actionInline}>
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              disabled={ed.status === "da_dang"}
                              onClick={() => handleEditorialDecision(ed, "da_dang")}
                            >
                              Duyệt đăng
                            </button>
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              disabled={ed.status === "can_chinh_sua"}
                              onClick={() => handleEditorialDecision(ed, "can_chinh_sua")}
                            >
                              Yêu cầu sửa
                            </button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {content.editorialSubmissions.map((ed) => {
                const meta = EDITORIAL_STATUS_META[ed.status];
                return (
                  <div key={ed.id} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>{ed.title}</span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>
                        {ed.shopName} · {ed.embeddedSkuCount} SKU nhúng
                      </span>
                      {ed.status !== "da_dang" && (
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={() => handleEditorialDecision(ed, "da_dang")}
                        >
                          Duyệt đăng
                        </button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </section>

          {/* 3D: Duyệt nhãn Pre-order / Gian hàng chính hãng / Đổi trả 7 ngày */}
          <section className={shared.panel} aria-labelledby="nd-badge-heading">
            <div className={shared.panelHeader}>
              <h2 id="nd-badge-heading" className={shared.panelTitle}>
                Duyệt nhãn Pre-order, Gian hàng chính hãng & Đổi trả 7 ngày
              </h2>
              <span className={shared.panelMeta}>
                {content.badgeRequests.filter((x) => x.status === "cho_duyet").length} hồ sơ chờ
              </span>
            </div>

            <div className={shared.tableWrap}>
              <table className={shared.dataTable}>
                <thead>
                  <tr>
                    <th>Gian hàng & phạm vi áp dụng</th>
                    <th>Nhãn đề nghị</th>
                    <th>Căn cứ kiểm định</th>
                    <th>Trạng thái</th>
                    <th className={shared.colAction}>Thao tác</th>
                  </tr>
                </thead>
                <tbody>
                  {content.badgeRequests.map((req) => {
                    const meta = BADGE_STATUS_META[req.status];
                    return (
                      <tr key={req.id}>
                        <td>
                          <div className={shared.cellPrimary}>{req.shopName}</div>
                          <div className={shared.cellSub}>
                            {req.code} · {req.targetScope}
                          </div>
                        </td>
                        <td>{BADGE_KIND_LABELS[req.badgeKind]}</td>
                        <td>
                          <div>{req.evidenceSummary}</div>
                          <div className={shared.cellSub}>{req.note}</div>
                        </td>
                        <td>
                          <StatusPill label={meta.label} tone={meta.tone} />
                        </td>
                        <td className={shared.colAction}>
                          <div className={styles.actionInline}>
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              disabled={req.status === "da_cap"}
                              onClick={() => handleBadgeDecision(req, "da_cap")}
                            >
                              Cấp nhãn
                            </button>
                            <button
                              type="button"
                              className={shared.rowActionBtn}
                              disabled={req.status === "tu_choi"}
                              onClick={() => handleBadgeDecision(req, "tu_choi")}
                            >
                              Từ chối
                            </button>
                          </div>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={shared.mobileList}>
              {content.badgeRequests.map((req) => {
                const meta = BADGE_STATUS_META[req.status];
                return (
                  <div key={req.id} className={shared.mobileItem}>
                    <div className={shared.mobileLine1}>
                      <span className={shared.mobileIdentity}>
                        {req.shopName} · {BADGE_KIND_LABELS[req.badgeKind]}
                      </span>
                      <StatusPill label={meta.label} tone={meta.tone} />
                    </div>
                    <div className={shared.mobileLine2}>
                      <span className={shared.mobileMetaText}>{req.targetScope}</span>
                      {req.status === "cho_duyet" && (
                        <button
                          type="button"
                          className={shared.rowActionBtn}
                          onClick={() => handleBadgeDecision(req, "da_cap")}
                        >
                          Cấp nhãn
                        </button>
                      )}
                    </div>
                  </div>
                );
              })}
            </div>
          </section>
        </div>
      )}
    </div>
  );
}

function AdminContentRoute() {
  return (
    <AdminStateGate
      title="Nội dung, danh mục & chiến dịch"
      subtitle="Quản trị điều hướng toàn site, Taxonomy 3 cấp, từ điển chất liệu, xếp hạng tìm kiếm đa shop và chiến dịch Sợi Xanh Nexus."
      emptyTitle="Chưa có cấu hình nội dung nào"
      emptyMessage="Tất cả mục điều hướng, danh mục và chiến dịch sàn chưa được khởi tạo."
      errorTitle="Không thể tải dữ liệu quản trị nội dung"
      errorMessage="Dịch vụ CMS và chỉ mục tìm kiếm tạm gián đoạn. Vui lòng thử tải lại."
      loadingRows={5}
    >
      {({ initialQuery }) => <AdminContentWorkspace initialQuery={initialQuery} />}
    </AdminStateGate>
  );
}

export default function AdminContentPage() {
  return (
    <Suspense fallback={<AdminSuspenseFallback title="Nội dung, danh mục & chiến dịch" />}>
      <AdminContentRoute />
    </Suspense>
  );
}
