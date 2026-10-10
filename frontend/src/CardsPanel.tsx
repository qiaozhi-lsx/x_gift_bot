import { useCallback, useEffect, useState } from "react";
import {
  Alert,
  Box,
  Button,
  Chip,
  Dialog,
  DialogActions,
  DialogContent,
  DialogContentText,
  DialogTitle,
  IconButton,
  LinearProgress,
  Paper,
  Stack,
  TextField,
  Tooltip,
  Typography,
} from "@mui/material";
import DeleteOutline from "@mui/icons-material/DeleteOutline";
import LockOpenOutlined from "@mui/icons-material/LockOpenOutlined";
import RefreshRounded from "@mui/icons-material/RefreshRounded";
import AddRounded from "@mui/icons-material/AddRounded";
import CreditCardOutlined from "@mui/icons-material/CreditCardOutlined";
import {
  addCard,
  listCards,
  removeCard,
  unblockCards,
  type CardStatus,
  type NewCard,
} from "./adminApi";
import { planLabel } from "./codeStatus";

const numberPattern = /^[0-9]{12,19}$/;
const monthPattern = /^(0[1-9]|1[0-2])$/;
const yearPattern = /^[0-9]{4}$/;
const cvcPattern = /^[0-9]{3,4}$/;

type ConfirmTarget =
  | { kind: "remove"; last4: string }
  | { kind: "unblock" }
  | null;

export function CardsPanel() {
  const [cards, setCards] = useState<CardStatus[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [addOpen, setAddOpen] = useState(false);
  const [confirm, setConfirm] = useState<ConfirmTarget>(null);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const data = await listCards();
      setCards(data.cards);
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => {
    void refresh();
  }, [refresh]);

  async function doRemove(last4: string) {
    setBusy(true);
    try {
      const r = await removeCard(last4);
      setNotice(r.message ?? "卡已移除。");
      await refresh();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
      setConfirm(null);
    }
  }
  async function doUnblock() {
    setBusy(true);
    try {
      const r = await unblockCards();
      setNotice(r.message ?? "已解除卡池冷却/封禁。");
      await refresh();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
      setConfirm(null);
    }
  }

  return (
    <Paper variant="outlined" sx={{ p: 3, mb: 3 }}>
      <Stack
        direction="row"
        alignItems="center"
        justifyContent="space-between"
        sx={{ mb: 1.5 }}
      >
        <Typography variant="h2" sx={{ display: "flex", alignItems: "center", gap: 1 }}>
          <CreditCardOutlined fontSize="small" />
          支付卡池
        </Typography>
        <Stack direction="row" spacing={1}>
          <Tooltip title="刷新">
            <span>
              <IconButton
                aria-label="刷新卡池"
                disabled={loading || busy}
                onClick={() => void refresh()}
                sx={{ border: 1, borderColor: "divider", width: 40, height: 40 }}
              >
                <RefreshRounded fontSize="small" />
              </IconButton>
            </span>
          </Tooltip>
          <Button
            size="small"
            variant="outlined"
            startIcon={<LockOpenOutlined />}
            disabled={loading || busy}
            onClick={() => setConfirm({ kind: "unblock" })}
          >
            解除冷却
          </Button>
          <Button
            size="small"
            variant="contained"
            startIcon={<AddRounded />}
            disabled={loading || busy}
            onClick={() => setAddOpen(true)}
          >
            添加卡
          </Button>
        </Stack>
      </Stack>
      <Typography variant="body2" color="text.secondary" sx={{ mb: 2 }}>
        初始额度耗尽的卡会自动跳过。已用金额由"已完成"兑换码实时聚合。
      </Typography>
      {loading && <LinearProgress sx={{ mb: 2 }} />}
      {error && (
        <Alert severity="error" sx={{ mb: 2 }} onClose={() => setError("")}>
          {error}
        </Alert>
      )}
      {notice && (
        <Alert severity="success" sx={{ mb: 2 }} onClose={() => setNotice("")}>
          {notice}
        </Alert>
      )}
      {cards.length === 0 && !loading ? (
        <Typography variant="body2" color="text.secondary">
          卡池为空。点"添加卡"录入第一张。
        </Typography>
      ) : (
        <Stack spacing={1.5}>
          {cards.map((c) => (
            <CardRow key={c.last4} card={c} onRemove={(last4) => setConfirm({ kind: "remove", last4 })} disabled={busy} />
          ))}
        </Stack>
      )}
      <AddCardDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onAdded={async (msg) => {
          setAddOpen(false);
          setNotice(msg);
          await refresh();
        }}
      />
      <Dialog
        open={confirm !== null}
        onClose={() => !busy && setConfirm(null)}
        maxWidth="xs"
        fullWidth
      >
        <DialogTitle>
          {confirm?.kind === "remove"
            ? `移除卡 **** ${confirm.last4}`
            : "解除卡池冷却/封禁"}
        </DialogTitle>
        <DialogContent>
          <DialogContentText>
            {confirm?.kind === "remove"
              ? "该卡会从卡池移除,现有订单的历史消费记录保留。此操作不可撤销。"
              : "将清空所有卡的 cooldown 和封禁状态。误触发的卡会立刻回到可用状态。"}
          </DialogContentText>
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirm(null)} disabled={busy}>
            取消
          </Button>
          <Button
            variant="contained"
            color={confirm?.kind === "remove" ? "error" : "primary"}
            disabled={busy}
            onClick={() => {
              if (confirm?.kind === "remove") void doRemove(confirm.last4);
              else if (confirm?.kind === "unblock") void doUnblock();
            }}
          >
            确定
          </Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}

function CardRow({
  card,
  onRemove,
  disabled,
}: {
  card: CardStatus;
  onRemove: (last4: string) => void;
  disabled: boolean;
}) {
  const used = card.used_usd ?? 0;
  const limit = card.limit ?? 0;
  const remaining = card.remaining ?? 0;
  const perMonths = card.per_months ?? {};
  const perSummary = Object.entries(perMonths)
    .filter(([, n]) => n > 0)
    .map(([m, n]) => `${n} ${planLabel(Number(m))}`)
    .join(" + ");
  return (
    <Box
      sx={{
        p: 2,
        border: 1,
        borderColor: "divider",
        borderRadius: 2,
        bgcolor: "background.paper",
      }}
    >
      <Stack direction="row" alignItems="center" justifyContent="space-between" flexWrap="wrap" gap={1}>
        <Stack direction="row" alignItems="center" spacing={1} flexWrap="wrap">
          <Typography variant="body1" sx={{ fontFamily: "monospace", fontWeight: 600 }}>
            **** {card.last4}
          </Typography>
          {card.unlimited ? (
            <Chip size="small" variant="outlined" label="未设限" />
          ) : (
            <Typography variant="body2" color="text.secondary" sx={{ fontVariantNumeric: "tabular-nums" }}>
              可用 ${remaining.toFixed(2)} / 初始 ${limit.toFixed(2)}
            </Typography>
          )}
          {card.used_count ? (
            <Typography variant="caption" color="text.secondary">
              · 已用 {card.used_count} 单{perSummary ? ` (${perSummary})` : ""} · 消费 ${used.toFixed(2)}
            </Typography>
          ) : (
            <Typography variant="caption" color="text.secondary">
              · 尚未消费
            </Typography>
          )}
        </Stack>
        <Stack direction="row" alignItems="center" spacing={1}>
          {card.depleted ? (
            <Chip size="small" color="warning" variant="outlined" label="额度不足" />
          ) : card.blocked ? (
            <Chip size="small" color="warning" variant="outlined" label={card.cooling_seconds ? "冷却中" : "已封禁"} />
          ) : card.usable ? (
            <Chip size="small" color="success" variant="outlined" label="可用" />
          ) : (
            <Chip size="small" variant="outlined" label="不可用" />
          )}
          <Tooltip title="移除此卡">
            <span>
              <IconButton
                color="error"
                aria-label={`移除卡 **** ${card.last4}`}
                disabled={disabled}
                onClick={() => onRemove(card.last4)}
                sx={{ border: 1, borderColor: "divider", width: 40, height: 40 }}
              >
                <DeleteOutline fontSize="small" />
              </IconButton>
            </span>
          </Tooltip>
        </Stack>
      </Stack>
      {card.problem && (
        <Typography variant="caption" color="error.main" component="p" sx={{ mt: 1 }}>
          {card.problem}
        </Typography>
      )}
      {card.blocked && !card.depleted && (
        <Typography variant="caption" color="text.secondary" component="p" sx={{ mt: 0.5 }}>
          {card.cooling_seconds
            ? `冷却剩余 ${Math.ceil(card.cooling_seconds / 60)} 分钟 · 原因:${card.blocked}`
            : `封禁原因:${card.blocked}`}
        </Typography>
      )}
    </Box>
  );
}

function AddCardDialog({
  open,
  onClose,
  onAdded,
}: {
  open: boolean;
  onClose: () => void;
  onAdded: (message: string) => Promise<void> | void;
}) {
  const [number, setNumber] = useState("");
  const [month, setMonth] = useState("");
  const [year, setYear] = useState("");
  const [cvc, setCvc] = useState("");
  const [limit, setLimit] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  function reset() {
    setNumber("");
    setMonth("");
    setYear("");
    setCvc("");
    setLimit("");
    setError("");
  }

  async function submit() {
    const trimmed = number.replace(/\s+/g, "");
    if (!numberPattern.test(trimmed)) {
      setError("卡号必须是 12-19 位数字。");
      return;
    }
    if (!monthPattern.test(month)) {
      setError("有效月份必须是 01-12。");
      return;
    }
    if (!yearPattern.test(year)) {
      setError("有效年份必须是 4 位数字(如 2028)。");
      return;
    }
    if (!cvcPattern.test(cvc)) {
      setError("CVV 必须是 3 或 4 位数字。");
      return;
    }
    const limitNum = Number(limit);
    if (!Number.isFinite(limitNum) || limitNum <= 0) {
      setError("初始额度(USD)必填,且必须大于 0。");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const payload: NewCard = {
        number: trimmed,
        exp_month: month,
        exp_year: year,
        cvc,
        limit: limitNum,
      };
      const r = await addCard(payload);
      await onAdded(r.message ?? "卡已添加。");
      reset();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      open={open}
      onClose={() => {
        if (busy) return;
        onClose();
        reset();
      }}
      fullWidth
      maxWidth="xs"
    >
      <Box
        component="form"
        noValidate
        onSubmit={(e) => {
          e.preventDefault();
          void submit();
        }}
      >
        <DialogTitle>添加支付卡</DialogTitle>
        <DialogContent>
          <DialogContentText sx={{ mb: 2 }}>
            姓名、邮箱、国家等 billing 字段会从已有卡自动继承。
          </DialogContentText>
          <Stack spacing={2}>
            <TextField
              label="卡号"
              value={number}
              onChange={(e) => setNumber(e.target.value)}
              inputProps={{ inputMode: "numeric", autoComplete: "off" }}
              required
              fullWidth
            />
            <Stack direction="row" spacing={2}>
              <TextField
                label="月"
                value={month}
                onChange={(e) => setMonth(e.target.value)}
                placeholder="12"
                inputProps={{ inputMode: "numeric", maxLength: 2, autoComplete: "off" }}
                required
                fullWidth
              />
              <TextField
                label="年"
                value={year}
                onChange={(e) => setYear(e.target.value)}
                placeholder="2028"
                inputProps={{ inputMode: "numeric", maxLength: 4, autoComplete: "off" }}
                required
                fullWidth
              />
              <TextField
                label="CVV"
                value={cvc}
                onChange={(e) => setCvc(e.target.value)}
                inputProps={{ inputMode: "numeric", maxLength: 4, autoComplete: "off" }}
                required
                fullWidth
              />
            </Stack>
            <TextField
              label="初始额度 (USD)"
              value={limit}
              onChange={(e) => setLimit(e.target.value)}
              placeholder="例如 50"
              inputProps={{ inputMode: "decimal", autoComplete: "off" }}
              helperText="必填。额度耗尽后卡会自动跳过。"
              required
              fullWidth
            />
            {error && <Alert severity="error">{error}</Alert>}
          </Stack>
        </DialogContent>
        <DialogActions>
          <Button
            onClick={() => {
              onClose();
              reset();
            }}
            disabled={busy}
          >
            取消
          </Button>
          <Button type="submit" variant="contained" disabled={busy}>
            添加
          </Button>
        </DialogActions>
      </Box>
    </Dialog>
  );
}
