"use client";

import React, { Suspense, useMemo, useRef, useState } from "react";
import Image from "next/image";
import { useSearchParams } from "next/navigation";
import {
  COMPLAINT_SLA,
  HELP_FAQ_ITEMS,
  HELP_TOPIC_LABELS,
  PRODUCTS,
  SUPPORT_CHANNELS,
  type HelpTopicKey,
} from "@/data";
import { FABRIC_MEDIA, WORKSHOP_MEDIA } from "@/data/media";
import { normalizeVietnamese } from "@/lib/search";
import { formatDateTimeVi } from "@/lib/format";
import { useSession } from "@/context/SessionContext";
import { useMockStore, type SupportTicket } from "@/mock/MockStoreContext";
import { Button, IconChat, IconPhone, IconSpark, StateView } from "@/components";
import { ProductAddonStrip } from "@/features/commerce/ProductAddonStrip";
import { useRevealObserver } from "@/features/catalog/useRevealObserver";
import styles from "./page.module.css";

type ScopeFilter = "all" | "san" | "gian-hang";

const TICKET_KIND_LABELS: Record<SupportTicket["kind"], string> = {
  khieu_nai: "Khiếu nại đơn hàng",
  goi_lai: "Gọi lại trong 15 phút",
  bu_le_tat: "Bù lẻ chiếc tất thất lạc",
};

function HelpCenterContent() {
  const searchParams = useSearchParams();
  const rootRef = useRef<HTMLDivElement>(null);
  const rawState = searchParams.get("state");
  const viewState =
    rawState === "loading" || rawState === "empty" || rawState === "error" ? rawState : null;

  const { user } = useSession();
  const { supportTickets, submitSupportTicket, openChat, openAi } = useMockStore();

  // FAQ search & filter state
  const [faqQuery, setFaqQuery] = useState("");
  const [selectedTopic, setSelectedTopic] = useState<"all" | HelpTopicKey>("all");
  const [scopeFilter, setScopeFilter] = useState<ScopeFilter>("all");

  // 15-min missed-call callback form
  const [cbName, setCbName] = useState(user?.full_name ?? "");
  const [cbPhone, setCbPhone] = useState(user?.phone ?? "");
  const [cbNote, setCbNote] = useState("");
  const [cbError, setCbError] = useState<string | null>(null);

  // Formal complaint / missing-sock ticket form
  const [ticketKind, setTicketKind] = useState<"khieu_nai" | "bu_le_tat">("khieu_nai");
  const [tkOrderCode, setTkOrderCode] = useState("");
  const [tkName, setTkName] = useState(user?.full_name ?? "");
  const [tkPhone, setTkPhone] = useState(user?.phone ?? "");
  const [tkEmail, setTkEmail] = useState(user?.email ?? "");
  const [tkSubject, setTkSubject] = useState("");
  const [tkDetail, setTkDetail] = useState("");
  const [tkVulnerable, setTkVulnerable] = useState(false);
  const [tkError, setTkError] = useState<string | null>(null);

  useRevealObserver(
    rootRef,
    `${viewState ?? "live"}|${selectedTopic}|${scopeFilter}|${supportTickets.length}`
  );

  const suggestedProducts = useMemo(() => PRODUCTS.slice(8, 12), []);

  const filteredFaqs = useMemo(() => {
    const q = normalizeVietnamese(faqQuery);
    return HELP_FAQ_ITEMS.filter((item) => {
      if (selectedTopic !== "all" && item.topic !== selectedTopic) return false;
      if (scopeFilter !== "all" && item.scope !== scopeFilter) return false;
      if (!q) return true;
      const hay = normalizeVietnamese(
        `${item.question} ${item.answer} ${HELP_TOPIC_LABELS[item.topic]}`
      );
      return hay.includes(q);
    });
  }, [faqQuery, selectedTopic, scopeFilter]);

  const platformFaqs = useMemo(() => filteredFaqs.filter((f) => f.scope === "san"), [filteredFaqs]);
  const shopFaqs = useMemo(
    () => filteredFaqs.filter((f) => f.scope === "gian-hang"),
    [filteredFaqs]
  );

  const handleCallbackSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const digits = cbPhone.replace(/\D/g, "");
    if (cbName.trim().length < 2 || digits.length < 10) {
      setCbError("Vui lòng nhập họ tên và số điện thoại 10-11 chữ số để tổng đài gọi lại.");
      return;
    }
    setCbError(null);
    submitSupportTicket({
      kind: "goi_lai",
      fullName: cbName,
      phone: cbPhone,
      subject: "Yêu cầu gọi lại cuộc gọi nhỡ (15 phút)",
      detail: cbNote.trim() || "Khách hàng yêu cầu tổng đài 1900 6845 gọi lại tư vấn đơn hàng.",
      isVulnerablePriority: false,
    });
    setCbNote("");
  };

  const handleComplaintSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const digits = tkPhone.replace(/\D/g, "");
    if (tkName.trim().length < 2 || digits.length < 10) {
      setTkError("Vui lòng nhập đầy đủ họ tên và số điện thoại liên hệ.");
      return;
    }
    if (tkSubject.trim().length < 5 || tkDetail.trim().length < 10) {
      setTkError(
        "Vui lòng nhập tiêu đề (tối thiểu 5 ký tự) và nội dung chi tiết (tối thiểu 10 ký tự)."
      );
      return;
    }
    setTkError(null);
    submitSupportTicket({
      kind: ticketKind,
      orderCode: tkOrderCode || undefined,
      fullName: tkName,
      phone: tkPhone,
      email: tkEmail || undefined,
      subject: tkSubject,
      detail: tkDetail,
      isVulnerablePriority: tkVulnerable,
    });
    setTkOrderCode("");
    setTkSubject("");
    setTkDetail("");
    setTkVulnerable(false);
  };

  const renderHeader = (subtitle: string) => (
    <section className={styles.pageHeader}>
      <div className={styles.headerLeadGroup}>
        <h1 className={`${styles.pageTitle} lines`}>
          <span className="line">
            <span data-line="" style={{ "--i": 0 } as React.CSSProperties}>
              Trung tâm trợ giúp
            </span>
          </span>
        </h1>
        <p className={styles.pageLead} data-rise="" style={{ "--i": 1 } as React.CSSProperties}>
          {subtitle}
        </p>
      </div>
      <div className={styles.headerAside} data-rise="" style={{ "--i": 2 } as React.CSSProperties}>
        <Button variant="secondary" size="md" href="/tra-cuu-don-hang">
          Tra cứu đơn hàng
        </Button>
        <Button variant="primary" size="md" onClick={openAi}>
          <IconSpark size={15} />
          <span>Mở Trợ lý AI</span>
        </Button>
      </div>
    </section>
  );

  if (viewState === "loading") {
    return (
      <div ref={rootRef} className={styles.page}>
        {renderHeader("Đang tải thư viện hỏi đáp chính sách và kênh liên hệ hỗ trợ.")}
        <section className={styles.section}>
          <h2 className={styles.sectionTitle}>Thư viện câu hỏi thường gặp</h2>
          <StateView kind="loading" title="Đang tải trung tâm trợ giúp..." rows={4} />
        </section>
        <ProductAddonStrip title="Đồ cơ bản áp dụng đổi trả 7 ngày" products={suggestedProducts} />
      </div>
    );
  }

  if (viewState === "error") {
    return (
      <div ref={rootRef} className={styles.page}>
        {renderHeader("Không thể tải danh sách phiếu hỗ trợ lúc này.")}
        <section className={styles.section}>
          <h2 className={styles.sectionTitle}>Trạng thái kết nối</h2>
          <StateView
            kind="error"
            title="Chưa thể kết nối Trung tâm trợ giúp"
            message="Vui lòng tải lại hoặc gọi trực tiếp Hotline 1900 6845 (8:30 - 22:00 tất cả các ngày)."
            actionLabel="Tải lại trang"
            actionHref="/tro-giup"
          />
        </section>
        <ProductAddonStrip title="Đồ cơ bản nổi bật" products={suggestedProducts} />
      </div>
    );
  }

  if (viewState === "empty") {
    return (
      <div ref={rootRef} className={styles.page}>
        {renderHeader("Bạn chưa có yêu cầu hỗ trợ hay khiếu nại nào đang mở.")}
        <section className={styles.section}>
          <h2 className={styles.sectionTitle}>Danh sách phiếu hỗ trợ</h2>
          <StateView
            kind="empty"
            title="Chưa có phiếu hỗ trợ nào"
            message="Mở trang Trợ giúp để tra cứu câu hỏi thường gặp hoặc yêu cầu tổng đài gọi lại trong 15 phút."
            actionLabel="Mở Trung tâm trợ giúp"
            actionHref="/tro-giup"
          />
        </section>
        <ProductAddonStrip title="Đồ cơ bản chuẩn xưởng" products={suggestedProducts} />
      </div>
    );
  }

  return (
    <div ref={rootRef} className={styles.page}>
      {renderHeader(
        "Tra cứu chính sách đổi trả 7 ngày, giao hàng 34 tỉnh thành và liên hệ tổng đài 1900 6845."
      )}

      {/* 100% Satisfaction Commitment Band (dark slate) + Workshop Photography */}
      <section className={styles.section} aria-label="Cam kết hài lòng và đường dây nóng">
        <div className={styles.commitmentSplit}>
          <div
            className={styles.commitmentPanel}
            data-rise=""
            style={{ "--i": 2 } as React.CSSProperties}
          >
            <div className={styles.commitmentContent}>
              <h2 className={styles.commitmentTitle}>Cam kết hài lòng 100% trên NexusMart</h2>
              <ul className={styles.commitmentList}>
                {COMPLAINT_SLA.commitments.map((line) => (
                  <li key={line}>{line}</li>
                ))}
              </ul>
            </div>
            <div className={styles.commitmentBadges}>
              <span className={styles.commitmentChip}>
                Hotline 1900 6845 · {COMPLAINT_SLA.hotlineHours}
              </span>
              <span className={styles.commitmentChip}>
                Gọi lại cuộc gọi nhỡ trong {COMPLAINT_SLA.callbackMinutes} phút
              </span>
              <span className={styles.commitmentChip}>
                Khiếu nại: xác nhận {COMPLAINT_SLA.ackHours}h · xử lý{" "}
                {COMPLAINT_SLA.resolveDaysLabel}
              </span>
            </div>
          </div>

          <div
            className={styles.photoPair}
            data-rise=""
            style={{ "--i": 3 } as React.CSSProperties}
          >
            <div className={styles.photoFrame}>
              <Image
                src={WORKSHOP_MEDIA.src}
                alt={WORKSHOP_MEDIA.alt}
                fill
                priority
                sizes="(max-width: 1024px) 100vw, 40vw"
                className={styles.atmospherePhoto}
              />
            </div>
            <div className={styles.photoFrame}>
              <Image
                src={FABRIC_MEDIA.src}
                alt={FABRIC_MEDIA.alt}
                fill
                priority
                sizes="(max-width: 1024px) 100vw, 40vw"
                className={styles.atmospherePhoto}
              />
            </div>
          </div>
        </div>
      </section>

      {/* Searchable FAQ — single white filter bar + unboxed 2-column reading text (zero card-rows) */}
      <section className={styles.sectionGap} aria-labelledby="help-faq-heading">
        <div
          className={styles.sectionHead}
          data-cat-reveal=""
          style={{ "--stagger": 0 } as React.CSSProperties}
        >
          <h2 id="help-faq-heading" className={styles.sectionTitle}>
            Câu hỏi thường gặp (Sàn &amp; Gian hàng)
          </h2>
          <span className={`${styles.sectionNote} num`}>
            {filteredFaqs.length} / {HELP_FAQ_ITEMS.length} câu hỏi
          </span>
        </div>

        <div
          className={styles.faqControlBar}
          data-cat-reveal=""
          style={{ "--stagger": 1 } as React.CSSProperties}
        >
          <div className={styles.faqSearchRow}>
            <input
              type="search"
              className={styles.input}
              value={faqQuery}
              onChange={(e) => setFaqQuery(e.target.value)}
              placeholder="Tìm nhanh: đổi trả 7 ngày, freeship, hóa đơn VAT, bù lẻ tất..."
              aria-label="Tìm kiếm câu hỏi thường gặp"
            />
            <div className={styles.modeSwitch} role="group" aria-label="Lọc phạm vi chính sách">
              {(
                [
                  { id: "all", label: "Tất cả" },
                  { id: "san", label: "Chính sách sàn" },
                  { id: "gian-hang", label: "Chính sách gian hàng" },
                ] as Array<{ id: ScopeFilter; label: string }>
              ).map((s) => (
                <button
                  key={s.id}
                  type="button"
                  className={`${styles.modeBtn} ${scopeFilter === s.id ? styles.modeBtnActive : ""}`}
                  onClick={() => setScopeFilter(s.id)}
                >
                  {s.label}
                </button>
              ))}
            </div>
          </div>

          <div className={styles.topicBar} role="group" aria-label="Lọc theo chủ đề">
            <button
              type="button"
              className={`${styles.topicChip} ${
                selectedTopic === "all" ? styles.topicChipActive : ""
              }`}
              onClick={() => setSelectedTopic("all")}
            >
              Tất cả chủ đề
            </button>
            {(Object.keys(HELP_TOPIC_LABELS) as HelpTopicKey[]).map((key) => (
              <button
                key={key}
                type="button"
                className={`${styles.topicChip} ${
                  selectedTopic === key ? styles.topicChipActive : ""
                }`}
                onClick={() => setSelectedTopic(key)}
              >
                {HELP_TOPIC_LABELS[key]}
              </button>
            ))}
          </div>
        </div>

        {filteredFaqs.length === 0 ? (
          <StateView
            kind="empty"
            title="Không tìm thấy câu hỏi khớp từ khóa"
            message="Thử xóa bộ lọc hoặc gửi trực tiếp câu hỏi qua Chat xưởng, Trợ lý AI hay biểu mẫu bên dưới."
            actionLabel="Xem toàn bộ câu hỏi"
            onAction={() => {
              setFaqQuery("");
              setSelectedTopic("all");
              setScopeFilter("all");
            }}
          />
        ) : (
          <div className={styles.faqTwoCol}>
            {(scopeFilter === "all" || scopeFilter === "san") && (
              <div className={styles.faqScopeCol}>
                <div className={styles.scopeHead}>
                  <h3 className={styles.scopeTitle}>Chính sách sàn NexusMart</h3>
                  <span className={`${styles.countBadge} num`}>{platformFaqs.length} mục</span>
                </div>
                {platformFaqs.length === 0 ? (
                  <p className={styles.faqMeta}>Không có câu hỏi cấp sàn trong chủ đề này.</p>
                ) : (
                  <div className={styles.faqReadingList}>
                    {platformFaqs.map((item, idx) => (
                      <article
                        key={item.id}
                        className={styles.faqEntry}
                        data-cat-reveal=""
                        style={{ "--stagger": idx % 4 } as React.CSSProperties}
                      >
                        <span className={styles.faqMeta}>{HELP_TOPIC_LABELS[item.topic]}</span>
                        <p className={styles.faqQ}>{item.question}</p>
                        <p className={styles.faqA}>{item.answer}</p>
                      </article>
                    ))}
                  </div>
                )}
              </div>
            )}

            {(scopeFilter === "all" || scopeFilter === "gian-hang") && (
              <div className={styles.faqScopeCol}>
                <div className={styles.scopeHead}>
                  <h3 className={styles.scopeTitle}>Chính sách gian hàng &amp; Xưởng dệt</h3>
                  <span className={`${styles.countBadgePos} num`}>{shopFaqs.length} mục</span>
                </div>
                {shopFaqs.length === 0 ? (
                  <p className={styles.faqMeta}>Không có câu hỏi gian hàng trong chủ đề này.</p>
                ) : (
                  <div className={styles.faqReadingList}>
                    {shopFaqs.map((item, idx) => (
                      <article
                        key={item.id}
                        className={styles.faqEntry}
                        data-cat-reveal=""
                        style={{ "--stagger": idx % 4 } as React.CSSProperties}
                      >
                        <span className={styles.faqMeta}>{HELP_TOPIC_LABELS[item.topic]}</span>
                        <p className={styles.faqQ}>{item.question}</p>
                        <p className={styles.faqA}>{item.answer}</p>
                      </article>
                    ))}
                  </div>
                )}
              </div>
            )}
          </div>
        )}
      </section>

      {/* Unboxed Support Channels + Accent-Tint 15-min Callback Panel */}
      <section className={styles.sectionGap} aria-labelledby="help-channels-heading">
        <div className={styles.supportSplit}>
          <div>
            <div
              className={styles.sectionHead}
              data-cat-reveal=""
              style={{ "--stagger": 0 } as React.CSSProperties}
            >
              <h2 id="help-channels-heading" className={styles.sectionTitle}>
                Kênh hỗ trợ khách hàng
              </h2>
              <span className={styles.sectionNote}>Phục vụ 8:30 - 22:00 tất cả các ngày</span>
            </div>

            <div className={styles.channelList}>
              {SUPPORT_CHANNELS.map((ch, idx) => (
                <article
                  key={ch.id}
                  className={styles.channelRow}
                  data-cat-reveal=""
                  style={{ "--stagger": idx % 4 } as React.CSSProperties}
                >
                  <div className={styles.channelInfo}>
                    <span className={styles.channelTitle}>{ch.title}</span>
                    <span className={`${styles.channelVal} num`}>{ch.value}</span>
                    <span className={styles.faqMeta}>{ch.hours}</span>
                    <p className={styles.faqA}>{ch.note}</p>
                  </div>
                  <div className={styles.channelAction}>
                    {ch.actionType === "chat" && (
                      <Button variant="secondary" size="sm" onClick={() => openChat()}>
                        <IconChat size={14} />
                        <span>Mở Chat xưởng</span>
                      </Button>
                    )}
                    {ch.actionType === "ai" && (
                      <Button variant="secondary" size="sm" onClick={openAi}>
                        <IconSpark size={14} />
                        <span>Mở Trợ lý AI</span>
                      </Button>
                    )}
                    {ch.actionType === "callback" && (
                      <span className={styles.countBadgePos}>
                        <IconPhone size={13} />
                        <span>Gọi lại 15 phút</span>
                      </span>
                    )}
                  </div>
                </article>
              ))}
            </div>
          </div>

          {/* 15-minute missed call callback form — accent-tint band */}
          <form
            className={styles.callbackPanel}
            onSubmit={handleCallbackSubmit}
            noValidate
            data-cat-reveal=""
            style={{ "--stagger": 2 } as React.CSSProperties}
          >
            <h2 className={styles.panelTitle}>Yêu cầu gọi lại trong 15 phút</h2>
            <p className={styles.faqA}>
              Nếu bạn gọi tới Hotline <strong className="num">1900 6845</strong> lúc tổng đài bận
              hoặc muốn nhân viên chủ động liên hệ, hãy để lại số điện thoại. Chúng tôi gọi lại
              trong vòng <strong>{COMPLAINT_SLA.callbackMinutes} phút</strong> (8:30 - 22:00 T2 -
              CN).
            </p>
            <div className={styles.field}>
              <label htmlFor="cb-name" className={styles.fieldLabel}>
                Họ và tên
              </label>
              <input
                id="cb-name"
                type="text"
                className={styles.input}
                value={cbName}
                onChange={(e) => setCbName(e.target.value)}
              />
            </div>
            <div className={styles.field}>
              <label htmlFor="cb-phone" className={styles.fieldLabel}>
                Số điện thoại cần gọi lại
              </label>
              <input
                id="cb-phone"
                type="tel"
                className={styles.input}
                value={cbPhone}
                onChange={(e) => setCbPhone(e.target.value)}
              />
            </div>
            <div className={styles.field}>
              <label htmlFor="cb-note" className={styles.fieldLabel}>
                Nội dung cần hỗ trợ (không bắt buộc)
              </label>
              <textarea
                id="cb-note"
                className={styles.textarea}
                value={cbNote}
                onChange={(e) => setCbNote(e.target.value)}
                placeholder="Ví dụ: Cần giao gấp trong ngày tại TP.HCM cho đơn NM-261120-104."
              />
            </div>
            {cbError && (
              <p className={styles.fieldError} role="alert">
                {cbError}
              </p>
            )}
            <Button type="submit" variant="primary" size="md">
              Gửi yêu cầu gọi lại trong 15 phút
            </Button>
          </form>
        </div>
      </section>

      {/* Primary White Complaint Form + Unboxed Submitted Tickets */}
      <section className={styles.sectionGap} aria-labelledby="help-complaint-heading">
        <div className={styles.supportSplit}>
          <form
            className={styles.formPanel}
            onSubmit={handleComplaintSubmit}
            noValidate
            data-cat-reveal=""
            style={{ "--stagger": 0 } as React.CSSProperties}
          >
            <div className={styles.formHead}>
              <h2 id="help-complaint-heading" className={styles.panelTitle}>
                Gửi yêu cầu hỗ trợ &amp; bù lẻ tất
              </h2>
              <span className={styles.countBadge}>
                Xác nhận {COMPLAINT_SLA.ackHours}h · Xử lý {COMPLAINT_SLA.resolveDaysLabel}
              </span>
            </div>

            <div className={styles.formGrid}>
              <div className={styles.field}>
                <label htmlFor="tk-kind" className={styles.fieldLabel}>
                  Loại yêu cầu
                </label>
                <select
                  id="tk-kind"
                  className={styles.select}
                  value={ticketKind}
                  onChange={(e) => setTicketKind(e.target.value as "khieu_nai" | "bu_le_tat")}
                >
                  <option value="khieu_nai">Khiếu nại đơn hàng &amp; dịch vụ</option>
                  <option value="bu_le_tat">Yêu cầu bù lẻ 1 chiếc tất thất lạc</option>
                </select>
              </div>

              <div className={styles.field}>
                <label htmlFor="tk-order" className={styles.fieldLabel}>
                  Mã đơn hàng liên quan (nếu có)
                </label>
                <input
                  id="tk-order"
                  type="text"
                  className={styles.input}
                  value={tkOrderCode}
                  onChange={(e) => setTkOrderCode(e.target.value)}
                  placeholder="NM-261117-082"
                />
              </div>

              <div className={styles.field}>
                <label htmlFor="tk-name" className={styles.fieldLabel}>
                  Họ và tên
                </label>
                <input
                  id="tk-name"
                  type="text"
                  className={styles.input}
                  value={tkName}
                  onChange={(e) => setTkName(e.target.value)}
                />
              </div>

              <div className={styles.field}>
                <label htmlFor="tk-phone" className={styles.fieldLabel}>
                  Số điện thoại
                </label>
                <input
                  id="tk-phone"
                  type="tel"
                  className={styles.input}
                  value={tkPhone}
                  onChange={(e) => setTkPhone(e.target.value)}
                />
              </div>

              <div className={`${styles.field} ${styles.formFull}`}>
                <label htmlFor="tk-email" className={styles.fieldLabel}>
                  Email nhận kết quả xử lý
                </label>
                <input
                  id="tk-email"
                  type="email"
                  className={styles.input}
                  value={tkEmail}
                  onChange={(e) => setTkEmail(e.target.value)}
                />
              </div>

              <div className={`${styles.field} ${styles.formFull}`}>
                <label htmlFor="tk-subject" className={styles.fieldLabel}>
                  Tiêu đề ngắn gọn
                </label>
                <input
                  id="tk-subject"
                  type="text"
                  className={styles.input}
                  value={tkSubject}
                  onChange={(e) => setTkSubject(e.target.value)}
                  placeholder="Ví dụ: Giao thiếu 1 chiếc áo size L trong đơn NM-261120-104"
                />
              </div>

              <div className={`${styles.field} ${styles.formFull}`}>
                <label htmlFor="tk-detail" className={styles.fieldLabel}>
                  Nội dung chi tiết &amp; hướng giải quyết mong muốn
                </label>
                <textarea
                  id="tk-detail"
                  className={styles.textarea}
                  value={tkDetail}
                  onChange={(e) => setTkDetail(e.target.value)}
                  placeholder="Mô tả rõ tình trạng kiện hàng, sản phẩm hoặc nhu cầu đổi trả/hoàn tiền..."
                />
              </div>

              <div className={styles.formFull}>
                <div className={styles.priorityNotice}>
                  <label className={styles.checkboxRow}>
                    <input
                      type="checkbox"
                      className={styles.checkbox}
                      checked={tkVulnerable}
                      onChange={(e) => setTkVulnerable(e.target.checked)}
                    />
                    <span>
                      <strong>Ưu tiên nhóm yếu thế</strong> (phản hồi ưu tiên trong 4 giờ)
                    </span>
                  </label>
                  <p className={styles.priorityDesc}>{COMPLAINT_SLA.vulnerablePriorityNote}</p>
                </div>
              </div>
            </div>

            {tkError && (
              <p className={styles.fieldError} role="alert">
                {tkError}
              </p>
            )}

            <div className={styles.formActions}>
              <Button type="submit" variant="primary" size="md">
                Gửi phiếu hỗ trợ
              </Button>
            </div>
          </form>

          {/* Unboxed submitted tickets stream */}
          <div className={styles.ticketsCol}>
            <div
              className={styles.sectionHead}
              data-cat-reveal=""
              style={{ "--stagger": 1 } as React.CSSProperties}
            >
              <h2 className={styles.sectionTitle}>Phiếu hỗ trợ đã gửi ({supportTickets.length})</h2>
              <span className={styles.sectionNote}>Cập nhật tiến độ xử lý trực tiếp</span>
            </div>

            <div className={styles.ticketList}>
              {supportTickets.map((tk, idx) => (
                <article
                  key={tk.id}
                  className={styles.ticketEntry}
                  data-cat-reveal=""
                  style={{ "--stagger": idx + 2 } as React.CSSProperties}
                >
                  <div className={styles.ticketHead}>
                    <span className={`${styles.channelTitle} num`}>
                      {tk.id} · {TICKET_KIND_LABELS[tk.kind]}
                    </span>
                    <div className={styles.ticketTags}>
                      {tk.isVulnerablePriority && (
                        <span className={styles.countBadgeWarn}>Ưu tiên nhóm yếu thế</span>
                      )}
                      <span
                        className={
                          tk.status === "dang_xu_ly" ? styles.countBadgePos : styles.countBadge
                        }
                      >
                        {tk.status === "dang_xu_ly" ? "Đang xử lý" : "Đã tiếp nhận"}
                      </span>
                    </div>
                  </div>
                  <p className={styles.faqQ}>{tk.subject}</p>
                  <p className={styles.faqA}>{tk.detail}</p>
                  <p className={`${styles.faqMeta} num`}>
                    {tk.orderCode ? `Đơn ${tk.orderCode} · ` : ""}
                    {tk.slaLabel} · Gửi lúc {formatDateTimeVi(tk.createdAt)}
                  </p>
                </article>
              ))}
            </div>
          </div>
        </div>
      </section>

      <ProductAddonStrip
        title="An tâm chọn đồ basic với đổi trả 7 ngày"
        subtitle="Bốn thiết kế cơ bản áp dụng bưu tá đổi size 2 chiều miễn phí tận nơi"
        products={suggestedProducts}
      />
    </div>
  );
}

export default function HelpCenterPage() {
  return (
    <Suspense
      fallback={
        <div className={styles.page}>
          <StateView kind="loading" title="Đang tải Trung tâm trợ giúp..." rows={4} />
        </div>
      }
    >
      <HelpCenterContent />
    </Suspense>
  );
}
