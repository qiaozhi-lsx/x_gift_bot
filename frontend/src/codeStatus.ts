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

// 用后端返回的 catalog 价格 map (minor units,如 {3:245, 6:495}) 格式化成本。
// 回退时无 price 返回空串,admin.tsx 由此决定要不要显示成本列。
export function formatCost(
  months: number,
  planPrices: Record<string, number> | Record<number, number> | undefined,
): string {
  if (!planPrices) return "";
  const minor =
    (planPrices as Record<string, number>)[String(months)] ??
    (planPrices as Record<number, number>)[months];
  if (typeof minor !== "number" || !Number.isFinite(minor) || minor <= 0) return "";
  return `$${(minor / 100).toFixed(2)}`;
}

// 套餐短名,面向后台展示。3 月 = 季卡,6 月 = 半年卡。
export function planLabel(months: number): string {
  if (months === 3) return "季卡";
  if (months === 6) return "半年卡";
  return `${months} 个月`;
}
