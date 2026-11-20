"use client";

import React, { useMemo, useState } from "react";
import type { Product, Shop } from "@/data";
import { useStore } from "@/context/StoreContext";
import { useMockStore } from "@/mock/MockStoreContext";
import { useSession } from "@/components/SessionProvider";
import { Button, IconChat, IconSpark } from "@/components";
import { formatDateVi } from "@/lib/format";
import styles from "./PdpQuestions.module.css";

export interface PdpQuestionsProps {
  product: Product;
  shop: Shop;
}

export function PdpQuestions({ product, shop }: PdpQuestionsProps) {
  const { openChat, openAi } = useStore();
  const { productQAs, submitProductQA } = useMockStore();
  const { user } = useSession();
  const [askerName, setAskerName] = useState<string>(user?.fullName ?? "");
  const [question, setQuestion] = useState<string>("");
  const [error, setError] = useState<string>("");

  const qaList = useMemo(
    () => productQAs.filter((q) => q.productId === product.id),
    [productQAs, product.id]
  );

  const faqs = useMemo(() => {
    if (product.faqs && product.faqs.length > 0) return product.faqs;
    return [
      {
        question: "Sản phẩm có hỗ trợ đổi size tận nơi không?",
        answer:
          product.returnEligible === false
            ? (product.returnExclusionReason ??
              "Sản phẩm thuộc nhóm ngoại lệ không áp dụng đổi trả sau khi mở niêm phong.")
            : "Có. NexusMart hỗ trợ đổi trả miễn phí 45 ngày, shipper giao size mới và thu hồi hàng cũ tận nơi.",
      },
      {
        question: "Thời gian giao hàng từ kho xưởng mất bao lâu?",
        answer: `Hàng xuất trực tiếp từ kho ${shop.city || "xưởng đối tác"}, giao tiêu chuẩn 1-4 ngày làm việc toàn quốc 34 tỉnh và hỗ trợ đồng kiểm ngoại quan.`,
      },
    ];
  }, [product, shop.city]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (question.trim().length < 8) {
      setError("Vui lòng nhập câu hỏi tối thiểu 8 ký tự để xưởng hỗ trợ chính xác.");
      return;
    }
    submitProductQA({
      productId: product.id,
      askerName: askerName.trim() || user?.fullName || "Khách hàng NexusMart",
      question: question.trim(),
    });
    setQuestion("");
    setError("");
  };

  return (
    <section className={styles.section} aria-labelledby="pdp-qa-heading">
      <div className={styles.sectionHead}>
        <h2 id="pdp-qa-heading" className={styles.sectionHeading}>
          Câu hỏi thường gặp và hỏi đáp
        </h2>
        <p className={styles.headNote}>Hỏi trực tiếp {shop.shortName} hoặc Trợ lý AI</p>
      </div>

      <div className={styles.layoutGrid}>
        <div className={styles.faqCol}>
          <h3 className={styles.subHeading}>Câu hỏi thường gặp</h3>

          <div className={styles.faqTextList}>
            {faqs.map((item) => (
              <p key={item.question} className={styles.faqParagraph}>
                <strong className={styles.faqQuestion}>{item.question} </strong>
                <span>{item.answer}</span>
              </p>
            ))}
          </div>

          <div className={styles.directHelpBox}>
            <p className={styles.directHelpTitle}>Cần tư vấn nhanh về cỡ hoặc chất vải?</p>
            <p className={styles.directHelpDesc}>
              {shop.name} phản hồi trung bình {(shop.responseTime || "trong 15 phút").toLowerCase()}
              . Trợ lý AI gợi ý nhanh kích cỡ theo số đo và hỗ trợ tra cứu đơn hàng của bạn.
            </p>
            <div className={styles.directHelpBtns}>
              <Button variant="ink" size="md" onClick={() => openChat(shop.id)}>
                <IconChat size={15} />
                <span>Chat với xưởng</span>
              </Button>
              <Button variant="secondary" size="md" onClick={openAi}>
                <IconSpark size={15} />
                <span>Trợ lý AI</span>
              </Button>
            </div>
          </div>
        </div>

        <div className={styles.qaCol}>
          <div className={styles.qaHeadRow}>
            <h3 className={styles.subHeading}>{`Hỏi đáp công khai (${qaList.length})`}</h3>
            <span className={styles.qaSampleHint}>Bao gồm hỏi đáp mẫu minh họa</span>
          </div>

          <form className={styles.askForm} onSubmit={handleSubmit} noValidate>
            <div className={styles.askInputs}>
              <label className={styles.field}>
                <span className={styles.fieldLabel}>Tên của bạn</span>
                <input
                  type="text"
                  value={askerName}
                  onChange={(e) => setAskerName(e.target.value)}
                  className={styles.input}
                  placeholder="Tên hiển thị"
                />
              </label>

              <label className={styles.field}>
                <span className={styles.fieldLabel}>Đặt câu hỏi cho xưởng</span>
                <textarea
                  rows={2}
                  value={question}
                  onChange={(e) => {
                    setQuestion(e.target.value);
                    if (error) setError("");
                  }}
                  placeholder={`Hỏi ${shop.shortName} về chiều dài áo, độ dày vải, phối đồ hoặc xuất VAT...`}
                  className={styles.textarea}
                />
              </label>
            </div>

            {error && <p className={styles.errorText}>{error}</p>}

            <div className={styles.askFooter}>
              <Button type="submit" variant="primary" size="md">
                Đặt câu hỏi cho xưởng
              </Button>
            </div>
          </form>

          {qaList.length === 0 ? (
            <div className={styles.emptyBox}>
              <p className={styles.emptyText}>
                Chưa có câu hỏi công khai nào cho {product.shortName}. Hãy gửi câu hỏi đầu tiên cho{" "}
                {shop.shortName} ở khung trên.
              </p>
            </div>
          ) : (
            <div className={styles.qaList}>
              {qaList.map((qa) => (
                <article key={qa.id} className={styles.qaCard}>
                  <div className={styles.qaMeta}>
                    <span className={styles.askerName}>{qa.askerName}</span>
                    <span className={styles.qaDate}>{formatDateVi(qa.askedAt)}</span>
                  </div>
                  <p className={styles.qaQuestion}>{qa.question}</p>

                  {qa.answer ? (
                    <div className={styles.qaAnswerBox}>
                      <div className={styles.qaMeta}>
                        <span className={styles.answerShop}>{qa.answeredBy ?? shop.name}</span>
                        {qa.answeredAt && (
                          <span className={styles.qaDate}>{formatDateVi(qa.answeredAt)}</span>
                        )}
                      </div>
                      <p className={styles.qaAnswerText}>{qa.answer}</p>
                    </div>
                  ) : (
                    <p className={styles.pendingNote}>
                      Đã gửi tới {shop.name}, dự kiến phản hồi{" "}
                      {(shop.responseTime || "trong 15 phút").toLowerCase()}.
                    </p>
                  )}
                </article>
              ))}
            </div>
          )}
        </div>
      </div>
    </section>
  );
}
