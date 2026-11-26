// Functional flows for Platform root host (nexusmart.localhost).
// Strictly uses role + visible Vietnamese text locators only.
import { assert, CREDS, gotoPage, MAILPIT_URL, rootOrigin } from "./lib.mjs";

async function fetchMailpitOtp(email, timeoutMs = 15_000) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const searchRes = await fetch(
      `${MAILPIT_URL}/api/v1/search?query=${encodeURIComponent(email)}`,
    );
    if (searchRes.ok) {
      const data = await searchRes.json();
      const msg = (data.messages || [])[0];
      if (msg?.ID) {
        const msgRes = await fetch(
          `${MAILPIT_URL}/api/v1/message/${encodeURIComponent(msg.ID)}`,
        );
        if (msgRes.ok) {
          const full = await msgRes.json();
          const body = `${full.Subject || ""} ${full.Text || ""} ${full.HTML || ""}`;
          const m = body.match(/\b(\d{6})\b/);
          if (m) return m[1];
        }
      }
    }
    await new Promise((r) => setTimeout(r, 600));
  }
  return null;
}

export async function buildPlatformFlows({ browser, port }) {
  const origin = rootOrigin(port);
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    reducedMotion: "reduce",
  });
  const page = await ctx.newPage();

  const flows = [
    {
      name: "platform:login-valid-invalid",
      fn: async () => {
        await gotoPage(page, `${origin}/dang-nhap`);
        await page
          .getByRole("heading", { level: 1, name: /Đăng nhập/i })
          .waitFor({ state: "visible" });

        // Invalid password -> error alert visible
        await page.getByLabel(/^Email$/i).fill(CREDS.seller.email);
        await page.getByLabel(/^Mật khẩu$/i).fill("SaiMatKhau123!");
        await page.getByRole("button", { name: /^Đăng nhập$/i }).click();
        await page
          .getByRole("alert")
          .first()
          .waitFor({ state: "visible", timeout: 10_000 });

        // Valid password -> redirects to /seller
        await page.getByLabel(/^Mật khẩu$/i).fill(CREDS.seller.password);
        await page.getByRole("button", { name: /^Đăng nhập$/i }).click();
        await page.waitForURL(/\/seller/, { timeout: 15_000 });
        await page
          .getByRole("heading", { level: 1, name: /Tổng quan/i })
          .waitFor({ state: "visible" });

        // Clean logout so subsequent platform flows start logged out
        await page.evaluate(() =>
          fetch("/api/auth/logout", { method: "POST" }),
        );
        await page.waitForTimeout(300);
        return "sai mật khẩu báo lỗi + đúng mật khẩu vào /seller";
      },
    },
    {
      name: "platform:register-otp-mailpit",
      fn: async () => {
        const qaEmail = `qa-s9-${Date.now()}@example.com`;
        await gotoPage(page, `${origin}/dang-ky`);
        await page
          .getByRole("heading", { level: 1 })
          .first()
          .waitFor({ state: "visible" });

        await page.getByLabel(/Họ và tên/i).fill("QA-S9 Kiểm Thử");
        await page.getByLabel(/^Email$/i).fill(qaEmail);
        await page
          .getByLabel(/Mật khẩu \(|Mật khẩu$/i)
          .first()
          .fill("QaS9Pass123!");
        await page.getByLabel(/Nhập lại mật khẩu/i).fill("QaS9Pass123!");
        await page
          .getByRole("button", { name: /Tạo tài khoản|Đăng ký/i })
          .first()
          .click();

        // Expect OTP verification step visible
        await page
          .getByText(/mã xác|OTP|6 chữ số/i)
          .first()
          .waitFor({ state: "visible", timeout: 15_000 });

        const otp = await fetchMailpitOtp(qaEmail, 15_000);
        assert(
          otp && /^\d{6}$/.test(otp),
          `Không nhận được OTP 6 số ở Mailpit cho ${qaEmail}`,
        );
        return `OTP Mailpit=${otp}`;
      },
    },
    {
      name: "platform:forgot-password",
      fn: async () => {
        await gotoPage(page, `${origin}/quen-mat-khau`);
        await page
          .getByRole("heading", { level: 1, name: /Quên mật khẩu/i })
          .waitFor({ state: "visible" });
        await page.getByLabel(/email/i).first().fill(CREDS.buyer.email);
        await page
          .getByRole("button", { name: /Gửi liên kết đặt lại mật khẩu|Gửi/i })
          .first()
          .click();
        await page
          .getByRole("status")
          .or(page.getByText(/đã gửi|kiểm tra email|hộp thư/i))
          .first()
          .waitFor({ state: "visible", timeout: 12_000 });
        return "gửi yêu cầu đặt lại thành công";
      },
    },
    {
      name: "platform:redirects-308",
      fn: async () => {
        const cases = [
          {
            from: "/gian-hang/xuong-det-bach-dang",
            expectHost: "bach-dang.nexusmart.localhost",
            expectPath: "/",
          },
          {
            from: "/san-pham/combo-3-quan-lot-boxer-modal",
            expectHost: "bach-dang.nexusmart.localhost",
            expectPath: "/san-pham/combo-3-quan-lot-boxer-modal",
          },
        ];
        for (const c of cases) {
          const res = await fetch(`${origin}${c.from}`, { redirect: "manual" });
          assert(
            res.status === 308,
            `${c.from} trả HTTP ${res.status}, mong đợi 308`,
          );
          const loc = res.headers.get("location") || "";
          assert(
            loc.includes(c.expectHost) && loc.endsWith(c.expectPath),
            `${c.from} Location=${loc} không khớp ${c.expectHost}${c.expectPath}`,
          );
        }
        return "2 redirect 308 đúng đích";
      },
    },
    {
      name: "platform:help-faq",
      fn: async () => {
        await gotoPage(page, `${origin}/tro-giup`);
        await page
          .getByRole("heading", { level: 1, name: /Trợ giúp/i })
          .waitFor({ state: "visible" });
        const question = page
          .getByText("Làm thế nào để mở cửa hàng riêng trên NexusMart?")
          .first();
        await question.waitFor({ state: "visible", timeout: 8_000 });
        await question.click();
        await page
          .getByText(/Đăng ký và xác thực email qua mã 6 chữ số/i)
          .first()
          .waitFor({ state: "visible", timeout: 6_000 });
        return "mở câu hỏi FAQ thấy câu trả lời";
      },
    },
  ];

  return { flows, cleanup: () => ctx.close() };
}
