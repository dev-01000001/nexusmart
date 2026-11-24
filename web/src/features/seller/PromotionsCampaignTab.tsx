"use client";

import React, { useState } from "react";
import type { Product } from "@/data";
import type { CampaignSlotKind } from "@/data/seller";
import { Button } from "@/components";
import { formatInt } from "@/lib/format";
import { registerSellerCampaignSlot, useSellerSession } from "./sellerStore";
import common from "./SellerCommon.module.css";
import styles from "./PromotionsExtra.module.css";

interface Props {
  shopId: string;
  shopShortName: string;
  shopProducts: Product[];
  showCampaignForm: boolean;
  onCloseCampaignForm: () => void;
  onNotify: (msg: string) => void;
}

const CAMPAIGN_TEMPLATES: Array<{
  kind: CampaignSlotKind;
  name: string;
  slotLabel: string;
  defaultNote: string;
  period: string;
}> = [
  {
    kind: "soi_xanh_nexus",
    name: "Chiến dịch cộng đồng Sợi Xanh Nexus · Quý 4/2026",
    slotLabel: "BST Sợi Xanh Nexus (CSR)",
    defaultNote: "Trích 5% doanh thu vào quỹ Sợi Xanh Nexus, sao kê công khai theo tháng",
    period: "01/11 - 31/12/2026",
  },
  {
    kind: "chien_dich_san",
    name: "Landing Công nghệ vải & Đồ cơ bản NexusMart",
    slotLabel: "Trang công nghệ vải & lưới mua nhanh",
    defaultNote: "Đủ tồn kho 5 size S-2XL và đính kèm chứng nhận GSM/QC từ xưởng",
    period: "22/11 - 10/12/2026",
  },
  {
    kind: "quang_ba_bst",
    name: "Suất quảng bá BST nổi bật trên Mega-menu sàn",
    slotLabel: "Cột BST nổi bật trên Mega-menu",
    defaultNote: "Ưu tiên giao trong 24h và áp dụng chính sách đổi trả 7 ngày của sàn",
    period: "25/11 - 15/12/2026",
  },
];

export function PromotionsCampaignTab({
  shopId,
  shopShortName,
  shopProducts,
  showCampaignForm,
  onCloseCampaignForm,
  onNotify,
}: Props) {
  const session = useSellerSession();
  const regs = session.campaignRegs.filter((r) => r.shopId === shopId);

  const [tplIndex, setTplIndex] = useState(0);
  const [productId, setProductId] = useState(shopProducts[0]?.id ?? "");
  const [commitmentNote, setCommitmentNote] = useState(CAMPAIGN_TEMPLATES[0].defaultNote);
  const [filterKind, setFilterKind] = useState<"all" | CampaignSlotKind>("all");

  const effectiveProd = shopProducts.find((p) => p.id === productId) ?? shopProducts[0];
  const filteredRegs = filterKind === "all" ? regs : regs.filter((r) => r.kind === filterKind);

  const kindLabel = (kind: CampaignSlotKind) =>
    kind === "soi_xanh_nexus"
      ? "Sợi Xanh Nexus"
      : kind === "quang_ba_bst"
        ? "Quảng bá BST"
        : "Chiến dịch sàn";

  const statusPill = (st: "dang_chay" | "da_duyet" | "cho_duyet") =>
    st === "dang_chay"
      ? { label: "Đang hiển thị", cls: `${common.pill} ${common.pillPos}` }
      : st === "da_duyet"
        ? { label: "Đã duyệt suất", cls: `${common.pill} ${common.pillPos}` }
        : { label: "Chờ Admin duyệt", cls: `${common.pill} ${common.pillWarn}` };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!effectiveProd) return;
    const tpl = CAMPAIGN_TEMPLATES[tplIndex] ?? CAMPAIGN_TEMPLATES[0];
    registerSellerCampaignSlot({
      id: `cmp-reg-${Date.now()}`,
      shopId,
      kind: tpl.kind,
      campaignName: tpl.name,
      slotLabel: tpl.slotLabel,
      productId: effectiveProd.id,
      productName: effectiveProd.shortName,
      commitmentNote: commitmentNote.trim() || tpl.defaultNote,
      status: "cho_duyet",
      periodLabel: tpl.period,
    });
    onCloseCampaignForm();
    onNotify(`Đã gửi đăng ký "${effectiveProd.shortName}" vào ${tpl.name} (chờ Admin duyệt suất).`);
  };

  return (
    <div className={styles.sectionStack}>
      <section className={common.card} role="note" aria-label="Thông báo dữ liệu mẫu">
        <span className={`${common.pill} ${common.pillWarn}`}>Bản xem trước, dữ liệu mẫu</span>
        <p className={common.cardMeta} style={{ marginTop: 8 }}>
          Khu vực đăng ký chiến dịch và bộ sưu tập sàn hiện sử dụng dữ liệu minh họa trên trình
          duyệt; các khuyến mãi đang vận hành thật nằm ở 3 tab Voucher gian hàng, Chương trình giảm
          giá shop và Đăng ký Flash sale.
        </p>
      </section>
      <section className={common.balanceStrip3} aria-label="Tổng hợp chiến dịch và BST sàn">
        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>Suất đang hiển thị / đã duyệt</span>
          <span className={common.balanceValue}>
            {formatInt(regs.filter((r) => r.status !== "cho_duyet").length)}
          </span>
          <span className={common.balanceCaption}>Trên trang chủ, Mega-menu & Sợi Xanh Nexus</span>
        </div>
        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>SKU tham gia Sợi Xanh Nexus</span>
          <span className={common.balanceValue}>
            {formatInt(regs.filter((r) => r.kind === "soi_xanh_nexus").length)}
          </span>
          <span className={common.balanceCaption}>Đóng góp minh bạch 5% vào quỹ cộng đồng</span>
        </div>
        <div className={common.balanceCell}>
          <span className={common.balanceLabel}>Đang chờ Admin duyệt</span>
          <span className={common.balanceValue}>
            {formatInt(regs.filter((r) => r.status === "cho_duyet").length)}
          </span>
          <span className={common.balanceCaption}>
            Kiểm tra tồn kho chuẩn và tỷ lệ giao đúng hạn
          </span>
        </div>
      </section>

      {showCampaignForm && (
        <form
          className={styles.inlineFormCard}
          onSubmit={handleSubmit}
          aria-label="Đăng ký SKU vào chiến dịch sàn"
        >
          <div className={common.cardHeader}>
            <h2 className={common.cardTitle}>
              Đăng ký SKU vào chiến dịch sàn / Sợi Xanh Nexus / BST Mega-menu
            </h2>
            <span className={common.cardMeta}>
              Gửi thẳng về hàng đợi duyệt chiến dịch của Admin
            </span>
          </div>

          <div className={styles.formGrid3}>
            <div className={common.field}>
              <label htmlFor="cmp-tpl" className={common.fieldLabel}>
                Chiến dịch hoặc suất quảng bá
              </label>
              <select
                id="cmp-tpl"
                className={common.textInput}
                value={tplIndex}
                onChange={(e) => {
                  const idx = Number(e.target.value);
                  setTplIndex(idx);
                  setCommitmentNote(CAMPAIGN_TEMPLATES[idx]?.defaultNote ?? "");
                }}
              >
                {CAMPAIGN_TEMPLATES.map((tpl, idx) => (
                  <option key={tpl.name} value={idx}>
                    {tpl.name}
                  </option>
                ))}
              </select>
            </div>
            <div className={common.field}>
              <label htmlFor="cmp-prod" className={common.fieldLabel}>
                Sản phẩm tham gia
              </label>
              <select
                id="cmp-prod"
                className={common.textInput}
                value={effectiveProd?.id ?? ""}
                onChange={(e) => setProductId(e.target.value)}
              >
                {shopProducts.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.shortName}
                  </option>
                ))}
              </select>
            </div>
            <div className={common.field}>
              <label htmlFor="cmp-note" className={common.fieldLabel}>
                Cam kết tồn kho & đóng góp
              </label>
              <input
                id="cmp-note"
                type="text"
                className={common.textInput}
                value={commitmentNote}
                onChange={(e) => setCommitmentNote(e.target.value)}
              />
            </div>
          </div>

          <div className={styles.formActions}>
            <Button variant="outline" size="sm" shape="app" onClick={onCloseCampaignForm}>
              Hủy
            </Button>
            <Button type="submit" variant="ink" size="sm" shape="app">
              Gửi đăng ký duyệt suất
            </Button>
          </div>
        </form>
      )}

      <section className={common.card} aria-labelledby="campaign-list-heading">
        <div className={common.cardHeader}>
          <h2 id="campaign-list-heading" className={common.cardTitle}>
            Danh sách đăng ký chiến dịch của {shopShortName}
          </h2>
          <div className={common.segmented} role="tablist" aria-label="Lọc loại chiến dịch">
            {(
              [
                ["all", "Tất cả"],
                ["soi_xanh_nexus", "Sợi Xanh Nexus"],
                ["chien_dich_san", "Chiến dịch sàn"],
                ["quang_ba_bst", "Quảng bá BST"],
              ] as const
            ).map(([k, lbl]) => (
              <button
                key={k}
                type="button"
                role="tab"
                aria-selected={filterKind === k}
                className={`${common.segmentedBtn} ${filterKind === k ? common.segmentedBtnActive : ""}`}
                onClick={() => setFilterKind(k)}
              >
                {lbl}
              </button>
            ))}
          </div>
        </div>

        {filteredRegs.length === 0 ? (
          <div className={common.inlineEmpty}>
            Chưa có sản phẩm nào đăng ký trong nhóm chiến dịch này.
          </div>
        ) : (
          <>
            <div className={`${common.tableWrap} ${common.tableWrapDesktop}`}>
              <table className={common.dataTable}>
                <thead>
                  <tr>
                    <th>Chiến dịch & vị trí hiển thị</th>
                    <th>Nhóm</th>
                    <th>Sản phẩm</th>
                    <th>Cam kết gian hàng</th>
                    <th>Thời gian</th>
                    <th>Trạng thái</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredRegs.map((reg) => {
                    const st = statusPill(reg.status);
                    return (
                      <tr key={reg.id}>
                        <td>
                          <span>{reg.campaignName}</span>
                          <span className={common.cellSub}>{reg.slotLabel}</span>
                        </td>
                        <td>{kindLabel(reg.kind)}</td>
                        <td>
                          <strong>{reg.productName}</strong>
                        </td>
                        <td>{reg.commitmentNote}</td>
                        <td>{reg.periodLabel}</td>
                        <td>
                          <span className={st.cls}>{st.label}</span>
                        </td>
                      </tr>
                    );
                  })}
                </tbody>
              </table>
            </div>

            <div className={common.mobileList}>
              {filteredRegs.map((reg) => {
                const st = statusPill(reg.status);
                return (
                  <div key={reg.id} className={common.mobileRow}>
                    <div className={common.mobileRowTop}>
                      <div>
                        <strong>{reg.campaignName}</strong>
                        <span className={common.cellSub}>
                          {reg.productName} · {reg.slotLabel}
                        </span>
                      </div>
                      <span className={st.cls}>{st.label}</span>
                    </div>
                    <div className={common.mobileRowMeta}>
                      <span>{kindLabel(reg.kind)}</span>
                      <span>{reg.periodLabel}</span>
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </section>
    </div>
  );
}
