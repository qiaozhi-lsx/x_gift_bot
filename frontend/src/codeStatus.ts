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

// Fixed Stripe payment cost per subscription duration (USD).
// 3 月 Premium = $2.45,6 月 Premium = $4.95.
export const costUSD: Record<number, number> = { 3: 2.45, 6: 4.95 };

export function formatCost(months: number): string {
  const v = costUSD[months];
  return v === undefined ? "" : `$${v.toFixed(2)}`;
}

// 套餐短名,面向后台展示。3 月 = 季卡,6 月 = 半年卡。
export function planLabel(months: number): string {
  if (months === 3) return "季卡";
  if (months === 6) return "半年卡";
  return `${months} 个月`;
}
