// Isolated UI preview: synthetic data only. No upstream API or payment access.
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { gzipSync } from "node:zlib";
import { randomBytes } from "node:crypto";

const port = Number(process.env.PREVIEW_PORT || 4173);
const redeemPage = process.argv.includes("--redeem");
const paused = process.env.PREVIEW_PAUSED === "true";
const states = new Map();
const attempts = new Map();
const linkJobs = new Map();
const redeemBatches = new Map();
const publicCodes = new Map();
const codePattern = /^(?:XG-[A-F0-9]{48}|XPP-[A-Z0-9]{16})$/;
const normalizeCode = (code) => typeof code === "string" ? code.trim().toUpperCase() : "";
const normalizeUsername = (username) => typeof username === "string" ? username.trim().replace(/^@/, "").toLowerCase() : "";
const maskUsername = (username) => !username ? "" : username.length < 4 ? "@***" : `@${username.slice(0, 2)}***${username.slice(-1)}`;
const fixtureTime = Math.floor(Date.now() / 1000);
for (const [code, status, queued = false] of [
  ["XPP-DEMOACTIVE000001", "active"],
  ["XPP-DEMOREDEEMED0001", "succeeded"],
  ["XPP-DEMOQUEUED000001", "processing", true],
  ["XPP-DEMOPROCESS00001", "processing"],
  ["XPP-DEMOREVIEW000001", "review"],
  ["XPP-DEMOREVOKED00001", "revoked"],
  ["XG-" + "5".repeat(48), "succeeded"],
]) publicCodes.set(code, { code, status, queued, ahead: queued ? 2 : 0, username: status === "active" || status === "revoked" ? "" : "demo_user", months: 3, progress: status === "succeeded" ? 100 : status === "processing" ? (queued ? 5 : 45) : 0, updated: fixtureTime });

function publicCode(raw) {
  const code = normalizeCode(raw);
  if (!codePattern.test(code)) return { http: 400, error: "invalid_code", message: "请填写完整兑换码。" };
  if (code === "XPP-DEMOBUSY00000001") return { http: 503, error: "service_unavailable", message: "暂时无法查询卡密，请稍后重试。" };
  let record = publicCodes.get(code);
  if (!record) {
    const generated = [...plaintext.entries()].find(([, value]) => value === code);
    const item = generated && codes.find((entry) => entry.id === generated[0]);
    if (item) {
      record = { code, status: item.status, months: item.months, username: item.username, updated: item.created, progress: 0, queued: false };
      publicCodes.set(code, record);
    }
  }
  if (!record) return { http: 404, error: "not_found", message: "未找到此卡密，请核对后重试。" };
  if (record.started) {
    const elapsed = Date.now() - record.started;
    record.queued = elapsed < 1800;
    record.status = elapsed >= 6500 ? "succeeded" : "processing";
    record.progress = record.queued ? 5 : record.status === "succeeded" ? 100 : Math.min(90, 20 + Math.floor(elapsed / 1000) * 10);
    record.updated = Math.floor(Date.now() / 1000);
  }
  return { http: 200, record };
}

function publicLookup(record) {
  const messages = {
    active: "卡密尚未兑换，可返回兑换页继续。",
    processing: "正在办理中，请稍后查询，无需重复提交。",
    succeeded: "此卡密已完成兑换，请登录接收账号查看 Premium。蓝色认证标识以 X 审核为准。",
    review: "兑换结果待核实，请保留卡密并联系购买商家，无需重复提交。",
    revoked: "此卡密已停用，请联系购买商家。",
  };
  return { status: record.status, months: record.months, label: `${record.months} 个月 Premium`, username_masked: maskUsername(record.username), progress: record.progress, updated: record.updated, queued: !!record.queued,
    ...(record.queued ? { ahead: record.ahead || 0 } : {}), message: record.queued ? `已排队，前面还有 ${record.ahead || 0} 单，请稍后查询。` : messages[record.status], preview_mode: true };
}
let folders = [
  { id: "a".repeat(32), name: "本地预览 · 示例批次" },
  { id: "b".repeat(32), name: "国庆活动" },
];
const plaintext = new Map();
const now = Math.floor(Date.now() / 1000);
let codes = ["active", "processing", "succeeded", "review", "revoked"].map(
  (status, index) => {
    // Two batches plus one unfiled code so client-side filtering is demonstrable.
    const folder = index < 2 ? folders[0] : index < 4 ? folders[1] : null;
    return {
      id: (index + 1).toString(16).padStart(32, "0"),
      folder: folder?.id ?? "",
      copyable: false,
      hint: `DEMO000${index}`,
      batch: folder?.name ?? "",
      months: index % 2 ? 3 : 6,
      status,
      username: index > 0 && index < 4 ? "demo_user" : "",
      message: status === "review" ? "示例：结果正在核实，请勿重复兑换。" : "",
      created: now - index * 3600,
    };
  },
);
const files = {
  "/": ["index.html", "text/html"],
  "/admin": ["admin.html", "text/html"],
  "/appearance.js": ["appearance.js", "application/javascript"],
  "/payment-notifications.js": ["payment-notifications.js", "application/javascript"],
  "/app.js": ["app.js", "application/javascript"],
  "/admin.js": ["admin.js", "application/javascript"],
  "/favicon.svg": ["favicon.svg", "image/svg+xml"],
  ...(redeemPage ? {
    "/redeem-favicon.svg": ["redeem-favicon.svg", "image/svg+xml"],
    "/redeem-favicon.ico": ["redeem-favicon.ico", "image/x-icon"],
  } : {}),
};

createServer(async (req, res) => {
  const nonce = randomBytes(16).toString("hex");
  res.setHeader("Cache-Control", "no-store");
  // Local visual preview: the browser's annotation overlay injects styles and
  // uses data-URL cursor images. Keep script restrictions and production CSP.
  const annotationStyles = redeemPage ? " style-src-elem 'self' 'unsafe-inline';" : "";
  const annotationImages = redeemPage ? " data:" : "";
  res.setHeader(
    "Content-Security-Policy",
    `default-src 'none'; script-src 'self' 'nonce-${nonce}' https://challenges.cloudflare.com; frame-src https://challenges.cloudflare.com; style-src 'self' 'nonce-${nonce}'; style-src-attr 'unsafe-inline';${annotationStyles} connect-src 'self'; worker-src 'self'; img-src 'self'${annotationImages}; base-uri 'none'; form-action 'self'; frame-ancestors 'none'`,
  );
  res.setHeader("X-Content-Type-Options", "nosniff");
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  function json(status, data) {
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(JSON.stringify(data));
  }
  try {
    if (url.pathname === "/api/security") {
      return json(200, { preview_mode: true, turnstile_enabled: process.env.PREVIEW_TURNSTILE === "true", turnstile_site_key: process.env.PREVIEW_TURNSTILE === "true" ? "1x00000000000000000000AA" : "" });
    }
    if (process.env.PREVIEW_TURNSTILE === "true" && req.method === "POST" && ["/api/check", "/api/redeem", "/api/manual-link", "/api/code-info", "/api/code-info-batch", "/api/code-lookup"].includes(url.pathname) && !req.headers["x-turnstile-token"]) {
      return json(403, { message: "请完成人机验证后重试。" });
    }
    if (req.method === "GET" && files[url.pathname]) {
      const [file, type] = files[url.pathname];
      const standaloneAsset = redeemPage && ["/", "/redeem-favicon.svg", "/redeem-favicon.ico"].includes(url.pathname);
      let data = await readFile(
        new URL(standaloneAsset ? `../../web-redeem/${file}` : `../../internal/site/assets/${file}`, import.meta.url),
      );
      if (type === "text/html")
        data = data
          .toString()
          .replaceAll("__XGIFT_NONCE__", nonce)
          .replace(/<script>/g, `<script nonce="${nonce}">`)
          .replace(/<style>/g, `<style nonce="${nonce}">`)
          .replace("</title>", " · 本地模拟预览</title>");
      res.setHeader("Vary", "Accept-Encoding");
      if (/\bgzip\b/.test(req.headers["accept-encoding"] || "")) {
        data = gzipSync(data, { level: 9 });
        res.setHeader("Content-Encoding", "gzip");
      }
      res.writeHead(200, { "Content-Type": type });
      res.end(data);
      return;
    }
    if (req.method === "GET" && ["/api/admin/manual-link/plans", "/api/manual-link/plans"].includes(url.pathname)) {
      json(200, { plans: [{months: 3, amount: 30000, currency: "BDT"}, {months: 6, amount: 60000, currency: "BDT"}] });
      return;
    }
    if (req.method === "GET" && url.pathname === "/api/manual-link/queue/current") {
      const latest = [...linkJobs.entries()].at(-1);
      return latest ? json(200, {ticket: latest[0], username:latest[1].username, months:latest[1].months}) : json(404, {});
    }
    if (req.method === "POST" && /^\/api\/manual-link\/queue\/[^/]+\/leave$/.test(url.pathname)) return json(200, {leaving:true});
    if (req.method === "POST" && /^\/api\/manual-link\/queue\/[^/]+\/cancel$/.test(url.pathname)) {
      linkJobs.delete(url.pathname.split("/").at(-2));
      return json(200, {cancelled:true});
    }
    if (req.method === "GET" && url.pathname.startsWith("/api/manual-link/queue/")) {
      const ticket = url.pathname.split("/").pop();
      const job = linkJobs.get(ticket);
      if (!job) { json(404, {message: "排队记录已失效，请重新提交。"}); return; }
      job.polls++;
      if (job.polls < 3 || process.env.PREVIEW_QUEUE_HOLD === "true") {
        json(202, {ticket, status: job.polls === 1 ? "queued" : "processing", position: 1, ahead: 0, estimated_wait_seconds: job.polls === 1 ? 20 : 10, message: job.polls === 1 ? "前方还有 0 人，预计约 20 秒后生成链接。请保持页面打开。" : "正在生成付款链接，预计还需约 10 秒。"}); return;
      }
      if (job.username === "expired_demo" && !job.verified_unpaid) {
        json(409, {message: "原付款链接已失效，请核实原订单未付款后再重新生成。", needs_unpaid_verification: true}); return;
      }
      json(200, {username: job.username, months: job.months, amount: job.months === 3 ? 30000 : 60000, currency: "BDT", status: "created", checkout_url: `https://checkout.stripe.com/c/pay/cs_test_PreviewOnly${ticket}`}); return;
    }
    if (req.method === "GET" && url.pathname === "/healthz") {
      json(200, { ok: true, payments_enabled: !paused });
      return;
    }
    if (req.method === "GET" && url.pathname.startsWith("/api/batch/")) {
      const batch = redeemBatches.get(url.pathname.split("/").pop());
      if (!batch) return json(404, { message: "查询批次不存在或已过期。" });
      const items = batch.map(({ code, username }) => {
        const { record } = publicCode(code);
        const result = publicLookup(record);
        return { hint: code.slice(-8), username, months: record.months, status: record.queued ? "queued" : record.status, progress: record.progress, ...(record.queued ? { ahead: record.ahead || 0 } : {}), message: `本地模拟：${result.message}` };
      });
      return json(200, { items, done: items.filter((item) => ["succeeded", "review", "failed"].includes(item.status)).length, total: items.length, payments_enabled: !paused, preview_mode: true });
    }
    if (req.method === "GET" && url.pathname === "/api/admin/codes") {
      const page = Math.max(0, Number(url.searchParams.get("page")) || 0);
      const folder = url.searchParams.get("folder") || "";
      if (
        folder &&
        folder !== "unfiled" &&
        !folders.some((item) => item.id === folder)
      ) {
        json(404, { message: "文件夹不存在，请刷新列表。" });
        return;
      }
      const filtered = codes.filter(
        (code) =>
          !folder ||
          (folder === "unfiled" ? !code.folder : code.folder === folder),
      );
      const stats = {
        total: codes.length,
        active: 0,
        processing: 0,
        succeeded: 0,
        review: 0,
        revoked: 0,
        unfiled: codes.filter((code) => !code.folder).length,
      };
      for (const code of codes) stats[code.status]++;
      json(200, {
        codes: filtered.slice(page * 100, (page + 1) * 100),
        page,
        folder,
        stats,
        folders: folders
          .map((item) => ({
            ...item,
            count: codes.filter((code) => code.folder === item.id).length,
          }))
          .sort((a, b) => a.name.localeCompare(b.name)),
        has_more: filtered.length > (page + 1) * 100,
        payments_enabled: !paused,
      });
      return;
    }
    if (req.method !== "POST") {
      if (req.method === "GET" && url.pathname === "/api/admin/recovery") {
        // PREVIEW_NETWORK=direct 可预览直连模式;默认节点池。
        const direct = process.env.PREVIEW_NETWORK === "direct";
        json(200, {
          batch: null,
          network: direct
            ? { mode: "direct", nodes: 0 }
            : { mode: "pool", nodes: 12, available: 9, cooling: 3 },
          cards: [
            { last4: "4242", usable: true },
            { last4: "1881", usable: true, cooling_seconds: 1500 },
            {
              last4: "0005",
              usable: true,
              blocked: "do_not_try_again",
              pair_cooling: 2,
            },
            { last4: "9917", usable: false, problem: "card has expired" },
          ],
          rotation: {
            batch_size: 3,
            used: 2,
            card_last4: "4242",
            node: direct ? "direct" : "node-1a2b3c4d5e6f",
          },
          paused: false,
          summary: {
            review: codes.filter((code) => code.status === "review").length,
            processing: codes.filter((code) => code.status === "processing")
              .length,
          },
        });
        return;
      }
      if (req.method === "GET" && url.pathname === "/api/admin/customer") {
        const key = url.searchParams.get("id") || "";
        const user = (url.searchParams.get("username") || "")
          .replace(/^@/, "")
          .toLowerCase();
        const found = codes.find(
          (code) =>
            (key && code.id === key) || (user && code.username === user),
        );
        if (!found) {
          json(404, { message: "没有找到对应的订单，请核对后重试。" });
          return;
        }
        json(200, {
          order: {
            id: found.id,
            username: found.username,
            hint: found.hint,
            months: found.months,
            status: found.status,
            message: found.message,
            batch: found.batch,
          },
          code: plaintext.get(found.id) || "",
          checkout_url: "",
          previous_checkout_url: "",
          can_recover: false,
          replacement_count: 0,
        });
        return;
      }
      if (req.method === "GET" && url.pathname === "/api/admin/stats") {
        const daily = [];
        for (let offset = 29; offset >= 0; offset--) {
          const day = new Date();
          day.setDate(day.getDate() - offset);
          const date = [
            day.getFullYear(),
            String(day.getMonth() + 1).padStart(2, "0"),
            String(day.getDate()).padStart(2, "0"),
          ].join("-");
          // Deterministic synthetic series with some zero days.
          const created = offset % 4 === 0 ? 0 : ((offset * 7) % 9) + 1;
          const redeemed = Math.max(0, created - (offset % 3));
          const succeeded = Math.max(0, redeemed - (offset % 2));
          daily.push({ date, created, redeemed, succeeded });
        }
        json(200, {
          codes: {
            total: 240,
            active: 62,
            processing: 8,
            review: 3,
            succeeded: 150,
            revoked: 17,
            redeemed: 161,
            unfiled: 12,
          },
          rates: { redeemed: 161 / 240, success: 150 / 161 },
          months: [
            { months: 3, total: 90, succeeded: 52 },
            { months: 6, total: 150, succeeded: 98 },
          ],
          daily,
          review_stages: [
            { progress: 20, count: 1 },
            { progress: 50, count: 1 },
            { progress: 90, count: 1 },
          ],
        });
        return;
      }
      json(404, { message: "预览路由不存在" });
      return;
    }
    let raw = "";
    for await (const chunk of req) {
      raw += chunk;
      if (raw.length > 4096) {
        json(413, { message: "请求过大" });
        return;
      }
    }
    const body = JSON.parse(raw);
    if (url.pathname === "/api/code-lookup" || url.pathname === "/api/code-info") {
      const found = publicCode(body.code);
      if (found.http !== 200) return json(found.http, { error: found.error, message: found.message });
      return json(200, publicLookup(found.record));
    }
    if (url.pathname === "/api/code-info-batch") {
      if (!Array.isArray(body.codes) || !body.codes.length || body.codes.length > 5) return json(400, { message: "每次最多校验 5 个兑换码。" });
      return json(200, { results: body.codes.map((code) => {
        const found = publicCode(code);
        return found.http === 200 ? { ok: true, ...publicLookup(found.record) } : { ok: false, error: found.error, retry: found.http === 503, message: found.message };
      }), preview_mode: true });
    }
    if (url.pathname === "/api/check-batch") {
      if (!Array.isArray(body.usernames) || !body.usernames.length || body.usernames.length > 5) return json(400, { message: "每次最多检测 5 个用户名。" });
      const users = body.usernames.map(normalizeUsername);
      if (users.some((username) => !/^[a-z0-9_]{1,15}$/.test(username))) return json(400, { message: "请填写正确的 X 用户名。" });
      return json(200, { results: users.map((username) => username === "demo_busy" ? { eligible: false, retry: true, message: "本地模拟：验证服务繁忙，请稍后重试，卡密未使用。" } : username === "existing_blue" ? { eligible: false, message: "本地模拟：该账号已有 Premium，无法接收赠送。" } : username === "missing_user" ? { eligible: false, message: "本地模拟：未找到该账号，请核对用户名。" } : { eligible: true, message: "本地模拟：该账号当前可以接收赠送。" }), preview_mode: true });
    }
    if (url.pathname === "/api/redeem-batch") {
      if (paused) return json(503, { status: "paused", message: "充值暂时暂停，请保留卡密。" });
      if (!Array.isArray(body.items) || !body.items.length || body.items.length > 5) return json(400, { message: "每次提交 1–5 组兑换码和用户名。" });
      const prepared = [];
      const seen = new Set();
      for (const item of body.items) {
        const code = normalizeCode(item?.code), username = normalizeUsername(item?.username);
        if (!/^[a-z0-9_]{1,15}$/.test(username) || seen.has(code)) return json(400, { message: "请核对账号，同一卡密不能重复提交。" });
        seen.add(code);
        const found = publicCode(code);
        if (found.http !== 200) return json(found.http, { message: found.message });
        if (found.record.status !== "active") return json(409, { message: "卡密正在处理、已兑换或已停用，请前往卡密查询查看原订单。" });
        if (["demo_busy", "existing_blue", "missing_user"].includes(username)) return json(409, { message: "账号尚未通过验证，卡密未使用。" });
        prepared.push({ code, username, record: found.record });
      }
      const token = randomBytes(16).toString("hex");
      const items = prepared.map(({ code, username, record }, index) => {
        Object.assign(record, { username, status: "processing", queued: true, ahead: index, progress: 5, started: Date.now(), updated: Math.floor(Date.now() / 1000) });
        return { hint: code.slice(-8), username, months: record.months };
      });
      redeemBatches.set(token, prepared.map(({ code, username }) => ({ code, username })));
      return json(202, { status: "processing", progress: 10, token, items, months: items[0].months, queued: items.length, message: "本地模拟：已加入处理队列，未执行真实付款。", preview_mode: true });
    }
    if (["/api/admin/manual-link", "/api/manual-link"].includes(url.pathname)) {
      if (![3, 6].includes(body.months) || !/^[a-z0-9_]{1,15}$/.test(body.username || "")) {
        json(400, {message: "请填写正确用户名和套餐。"}); return;
      }
      if (url.pathname === "/api/manual-link") {
        const ticket = randomBytes(12).toString("hex");
        linkJobs.set(ticket, {...body, polls: 0});
        json(202, {ticket, status: "queued", position: 2, ahead: 1, estimated_wait_seconds: 40, message: "前方还有 1 人，预计约 40 秒后生成链接。请保持页面打开。"}); return;
      }
      if (body.username === "expired_demo" && !body.verified_unpaid) {
        json(409, {message: "原付款链接已失效，请核实原订单未付款后再重新生成。", needs_unpaid_verification: true}); return;
      }
      json(200, {username: body.username, months: body.months, amount: body.months === 3 ? 30000 : 60000, currency: "BDT", status: "created", checkout_url: "https://checkout.stripe.com/c/pay/cs_test_ManualPreviewOnly"});
      return;
    }
    if (url.pathname.startsWith("/api/admin/recovery/")) {
      json(503, { message: "本地模拟预览不提供补单操作。" });
      return;
    }
    if (url.pathname === "/api/admin/folders" ||
      url.pathname === "/api/admin/folders/rename"
    ) {
      const name = typeof body.name === "string" ? body.name.trim() : "";
      if (!name || Buffer.byteLength(name) > 120) {
        json(400, { message: "请输入有效文件夹名称（最多 120 字节）。" });
        return;
      }
      if (
        folders.some(
          (folder) =>
            folder.name.toLowerCase() === name.toLowerCase() &&
            folder.id !== body.id,
        )
      ) {
        json(409, { message: "已存在同名文件夹。" });
        return;
      }
      if (url.pathname.endsWith("/rename")) {
        const folder = folders.find((item) => item.id === body.id);
        if (!folder) {
          json(404, { message: "文件夹不存在。" });
          return;
        }
        folder.name = name;
        for (const code of codes)
          if (code.folder === folder.id) code.batch = name;
        json(200, { message: "已重命名。" });
        return;
      }
      const folder = { id: randomBytes(16).toString("hex"), name };
      folders.push(folder);
      json(201, { ...folder, count: 0 });
      return;
    }
    if (url.pathname === "/api/admin/folders/delete") {
      if (!folders.some((folder) => folder.id === body.id)) {
        json(404, { message: "文件夹不存在。" });
        return;
      }
      folders = folders.filter((folder) => folder.id !== body.id);
      for (const code of codes)
        if (code.folder === body.id) {
          code.folder = "";
          code.batch = "";
        }
      json(200, { message: "文件夹已删除，兑换码已移至未分类。" });
      return;
    }
    if (url.pathname === "/api/admin/codes/move") {
      if (
        !Array.isArray(body.ids) ||
        !body.ids.length ||
        body.ids.length > 100 ||
        new Set(body.ids).size !== body.ids.length ||
        !body.ids.every((id) => codes.some((code) => code.id === id))
      ) {
        json(409, { message: "兑换码选择无效，请刷新列表。" });
        return;
      }
      if (body.folder && !folders.some((folder) => folder.id === body.folder)) {
        json(404, { message: "文件夹不存在。" });
        return;
      }
      for (const code of codes)
        if (body.ids.includes(code.id)) {
          code.folder = body.folder || "";
          code.batch = folders.find((f) => f.id === code.folder)?.name || "";
        }
      json(200, { message: "已更新分类。" });
      return;
    }
    if (url.pathname === "/api/admin/codes") {
      if (
        ![3, 6].includes(body.months) ||
        !Number.isInteger(body.count) ||
        body.count < 1 ||
        body.count > 500 ||
        typeof body.batch !== "string" ||
        Buffer.byteLength(body.batch) > 120
      ) {
        json(400, { message: "请检查套餐、数量和批次名称。" });
        return;
      }
      if (body.folder && !folders.some((folder) => folder.id === body.folder)) {
        json(404, { message: "文件夹不存在。" });
        return;
      }
      let batch = body.batch || "本地预览-" + Date.now();
      let folder = folders.find(
        (f) => f.name.toLowerCase() === batch.toLowerCase(),
      );
      if (!folder) {
        folder = { id: randomBytes(16).toString("hex"), name: batch };
        folders.push(folder);
      }
      batch = folder.name;
      body.folder = folder.id;
      const generated = Array.from(
        { length: body.count },
        () => "XG-" + randomBytes(24).toString("hex").toUpperCase(),
      );
      codes = [
        ...generated.map((code) => {
          const id = randomBytes(16).toString("hex");
          plaintext.set(id, code);
          return {
            id,
            copyable: true,
            folder: body.folder || "",
            hint: code.slice(-8),
            batch,
            months: body.months,
            status: "active",
            username: "",
            message: "",
            created: now,
          };
        }),
        ...codes,
      ];
      json(201, {
        codes: generated,
        batch,
        months: body.months,
        folder: body.folder || "",
      });
      return;
    }
    if (url.pathname === "/api/admin/lookup") {
      const full =
        typeof body.code === "string" ? body.code.trim().toUpperCase() : "";
      if (!/^XG-[A-F0-9]{48}$/.test(full)) {
        json(400, { message: "请填写 XG- 开头、后接 48 位字符的完整兑换码。" });
        return;
      }
      // Fixed demo hit so a found result can be previewed without generating.
      if (full === "XG-" + "5".repeat(48)) {
        json(200, { ...codes[2], progress: 100, updated: now });
        return;
      }
      const entry = [...plaintext.entries()].find(
        ([, value]) => value === full,
      );
      const found = entry && codes.find((code) => code.id === entry[0]);
      json(
        found ? 200 : 404,
        found
          ? { ...found, updated: found.created, progress: 0 }
          : { message: "没有找到这个兑换码，请核对后重试。" },
      );
      return;
    }
    if (url.pathname === "/api/admin/codes/copy") {
      const code = plaintext.get(body.id);
      json(
        code ? 200 : 409,
        code ? { code } : { message: "历史兑换码未保存完整内容。" },
      );
      return;
    }
    if (url.pathname === "/api/admin/revoke") {
      const code = codes.find((item) => item.id === body.id);
      if (!code || code.status !== "active") {
        json(409, { message: "只能停用未使用的兑换码。" });
        return;
      }
      code.status = "revoked";
      json(200, { message: "兑换码已停用。" });
      return;
    }
    if (url.pathname === "/api/check") {
      if (!/^[a-z0-9_]{1,15}$/.test(body.username || "")) {
        json(400, { message: "请填写正确的 X 用户名（不是显示名称）。" });
        return;
      }
      if (body.username === "blocked_user") {
        json(200, {
          eligible: false,
          message: "示例：X 当前不允许向这个账号赠送 Premium。",
        });
        return;
      }
      if (body.username === "busy_user") {
        json(503, { message: "暂时无法向 X 核实赠送资格，请稍后重试检测。" });
        return;
      }
      json(200, { eligible: true, message: "该账号当前可以接收赠送。" });
      return;
    }
    if (url.pathname === "/api/redeem" || url.pathname === "/api/status") {
      if (
        !/^XG-[A-F0-9]{48}$/.test(body.code) ||
        !/^[a-z0-9_]{1,15}$/.test(body.username)
      ) {
        json(400, { message: "请检查兑换码和用户名。" });
        return;
      }
      const ending = body.code.slice(-1);
      if (ending === "C") {
        json(422, {
          message: "示例：X 当前不允许向这个账号赠送 Premium。兑换码未使用。",
        });
        return;
      }
      if (ending === "D") {
        json(200, {
          status: "revoked",
          message: "兑换码已停用，请联系提供方。",
        });
        return;
      }
      if (req.url === "/api/redeem" && paused) {
        json(503, {
          status: "paused",
          message: "账号可以接收赠送，但充值服务暂未开放。兑换码未使用。",
        });
        return;
      }
      const key = body.code + ":" + body.username;
      if (req.url === "/api/redeem") {
        states.set(key, Date.now());
        attempts.set(key, (attempts.get(key) || 0) + 1);
      }
      if (!states.has(key)) {
        json(200, { status: "active", progress: 0, months: 6 });
        return;
      }
      const elapsed = Date.now() - states.get(key);
      if (ending === "E" && attempts.get(key) === 1 && elapsed > 3000) {
        json(200, {
          status: "review", progress: 50, months: 6,
          message: "本地模拟：原订单尚未付款，可重新检查并继续兑换。",
        });
        return;
      }
      if (ending === "B" && elapsed > 3000) {
        json(200, {
          status: "review",
          progress: 90,
          months: 6,
          message: "示例：结果暂未确认，请查询原订单或联系管理员。",
        });
        return;
      }
      const progress =
        ending === "F"
          ? 70
          : Math.min(100, 20 + Math.floor(elapsed / 1500) * 20);
      json(200, {
        status: progress === 100 ? "succeeded" : "processing",
        progress,
        months: 6,
        message:
          progress === 100
            ? `本地模拟：已为 @${body.username} 完成 6 个月 Premium 赠送（未执行真实付款）。`
            : "本地模拟：正在核验订单，请稍候。",
      });
      return;
    }
    json(404, { message: "预览路由不存在" });
  } catch {
    json(400, { message: "预览请求无法处理。" });
  }
}).listen(port, "127.0.0.1", () =>
  console.log(
    `本地模拟预览：http://127.0.0.1:${port} /admin；所有数据均为示例，不连接生产或付款服务。`,
  ),
);
