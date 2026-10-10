export const codeStatus: Record<
  string,
  { label: string; color: "default" | "primary" | "success" | "warning" }
> = {
  active: { label: "可使用", color: "primary" },
  processing: { label: "处理中", color: "primary" },
  succeeded: { label: "已完成", color: "success" },
  review: { label: "待核实", color: "warning" },
  revoked: { label: "已停用", color: "default" },
};

// 用后端返回的 "卡实付 USD" map(如 {3: 2.45, 6: 4.95}) 格式化成本。
// Stripe 账单货币(BDT)和卡实付货币(USD)不是一回事;这里只信后端的
// plan_costs_usd 字段。回退无价时返回空串,admin.tsx 据此决定要不要显示。
export function formatCost(
  months: number,
  planCostsUSD: Record<string, number> | Record<number, number> | undefined,
): string {
  if (!planCostsUSD) return "";
  const usd =
    (planCostsUSD as Record<string, number>)[String(months)] ??
    (planCostsUSD as Record<number, number>)[months];
  if (typeof usd !== "number" || !Number.isFinite(usd) || usd <= 0) return "";
  return `$${usd.toFixed(2)}`;
}

// 套餐短名,面向后台展示。3 月 = 季卡,6 月 = 半年卡。
export function planLabel(months: number): string {
  if (months === 3) return "季卡";
  if (months === 6) return "半年卡";
  return `${months} 个月`;
}
