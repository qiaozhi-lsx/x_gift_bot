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
const limitPattern = /^\d+(\.\d{1,2})?$/;
const countryPattern = /^[A-Z]{2}$/;
const emailPattern = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

const LIMIT_MIN = 1; // 单笔最便宜套餐就 $2.45,但老卡补额度可能设 1 块测试,放宽
const LIMIT_MAX = 100000;

type ConfirmTarget =
  | { kind: "remove"; last4: string }
  | { kind: "unblock-one"; last4: string; reason: string }
  | { kind: "unblock-all"; blocked: string[] }
  | null;

export function CardsPanel() {
  const [cards, setCards] = useState<CardStatus[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [addOpen, setAddOpen] = useState(false);
  const [confirm, setConfirm] = useState<ConfirmTarget>(null);
  const [lastConfirm, setLastConfirm] = useState<ConfirmTarget>(null); // 退场动画期间保留文案

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

  const openConfirm = (target: ConfirmTarget) => {
    setConfirm(target);
    if (target) setLastConfirm(target);
  };

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
  async function doUnblock(last4?: string) {
    setBusy(true);
    try {
      const r = await unblockCards(last4);
      setNotice(r.message ?? "已解除。");
      await refresh();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setBusy(false);
      setConfirm(null);
    }
  }

  const emptyPool = cards.length === 0;
  const blockedList = cards
    .filter((c) => c.blocked)
    .map((c) => `**** ${c.last4} (${c.blocked})`);

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
            disabled={loading || busy || blockedList.length === 0}
            onClick={() => openConfirm({ kind: "unblock-all", blocked: blockedList })}
          >
            全部解除冷却
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
        初始额度耗尽的卡会自动跳过。已用金额由"已完成 + 进行中"订单实时聚合。
        {emptyPool && " 卡池为空:添加第一张卡时必须完整填写姓名/邮箱/国家。"}
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
      {emptyPool && !loading ? (
        <Typography variant="body2" color="text.secondary">
          卡池为空。点"添加卡"录入第一张,记得填 billing 字段。
        </Typography>
      ) : (
        <Stack spacing={1.5}>
          {cards.map((c) => (
            <CardRow
              key={c.last4}
              card={c}
              disabled={busy}
              onRemove={(last4) => openConfirm({ kind: "remove", last4 })}
              onUnblock={(last4, reason) =>
                openConfirm({ kind: "unblock-one", last4, reason })
              }
            />
          ))}
        </Stack>
      )}
      <AddCardDialog
        open={addOpen}
        requireBilling={emptyPool}
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
        TransitionProps={{ onExited: () => setLastConfirm(null) }}
      >
        <ConfirmBody target={confirm ?? lastConfirm} />
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
              else if (confirm?.kind === "unblock-one") void doUnblock(confirm.last4);
              else if (confirm?.kind === "unblock-all") void doUnblock();
            }}
          >
            确定
          </Button>
        </DialogActions>
      </Dialog>
    </Paper>
  );
}

function ConfirmBody({ target }: { target: ConfirmTarget }) {
  if (!target) {
    return (
      <>
        <DialogTitle></DialogTitle>
        <DialogContent />
      </>
    );
  }
  if (target.kind === "remove") {
    return (
      <>
        <DialogTitle>{`移除卡 **** ${target.last4}`}</DialogTitle>
        <DialogContent>
          <DialogContentText>
            该卡会从卡池移除;历史消费记录保留。此操作不可撤销。
          </DialogContentText>
        </DialogContent>
      </>
    );
  }
  if (target.kind === "unblock-one") {
    return (
      <>
        <DialogTitle>{`解除卡 **** ${target.last4} 的冷却/封禁`}</DialogTitle>
        <DialogContent>
          <DialogContentText>
            {`当前原因:${target.reason || "未知"}。注意:do_not_try_again 等强信号是发卡行要求"永久不再尝试",强行解除可能导致 Stripe 账户层风控升级。`}
          </DialogContentText>
        </DialogContent>
      </>
    );
  }
  return (
    <>
      <DialogTitle>全部解除卡池冷却/封禁</DialogTitle>
      <DialogContent>
        <DialogContentText>当前被封禁/冷却的卡:</DialogContentText>
        <Box component="ul" sx={{ mt: 1, mb: 0, pl: 3 }}>
          {target.blocked.map((label) => (
            <li key={label}>
              <Typography variant="body2" sx={{ fontFamily: "monospace" }}>
                {label}
              </Typography>
            </li>
          ))}
        </Box>
        <DialogContentText sx={{ mt: 2 }}>
          所有卡的冷却和封禁记录都会被清掉,不可撤销。建议改用每张卡的"解除"按钮。
        </DialogContentText>
      </DialogContent>
    </>
  );
}

function CardRow({
  card,
  disabled,
  onRemove,
  onUnblock,
}: {
  card: CardStatus;
  disabled: boolean;
  onRemove: (last4: string) => void;
  onUnblock: (last4: string, reason: string) => void;
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
          {card.in_flight ? (
            <Chip size="small" color="info" variant="outlined" label={`进行中 ${card.in_flight}`} />
          ) : null}
        </Stack>
        <Stack direction="row" alignItems="center" spacing={1}>
          {card.depleted ? (
            <Chip size="small" color="warning" variant="outlined" label="额度不足" />
          ) : card.blocked ? (
            <Chip
              size="small"
              color="warning"
              variant="outlined"
              label={card.cooling_seconds ? "冷却中" : "已封禁"}
            />
          ) : card.pair_cooling ? (
            <Chip
              size="small"
              color="info"
              variant="outlined"
              label={`部分节点冷却 ${card.pair_cooling}`}
            />
          ) : card.usable ? (
            <Chip size="small" color="success" variant="outlined" label="可用" />
          ) : (
            <Chip size="small" variant="outlined" label="不可用" />
          )}
          {card.blocked && (
            <Tooltip title="解除此卡的冷却/封禁">
              <span>
                <IconButton
                  aria-label={`解除卡 **** ${card.last4} 冷却`}
                  disabled={disabled}
                  onClick={() => onUnblock(card.last4, card.blocked ?? "")}
                  sx={{ border: 1, borderColor: "divider", width: 40, height: 40 }}
                >
                  <LockOpenOutlined fontSize="small" />
                </IconButton>
              </span>
            </Tooltip>
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
  requireBilling,
  onClose,
  onAdded,
}: {
  open: boolean;
  requireBilling: boolean;
  onClose: () => void;
  onAdded: (message: string) => Promise<void> | void;
}) {
  const [number, setNumber] = useState("");
  const [month, setMonth] = useState("");
  const [year, setYear] = useState("");
  const [cvc, setCvc] = useState("");
  const [limit, setLimit] = useState("");
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [country, setCountry] = useState("");
  const [postal, setPostal] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  function reset() {
    setNumber("");
    setMonth("");
    setYear("");
    setCvc("");
    setLimit("");
    setName("");
    setEmail("");
    setCountry("");
    setPostal("");
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
    const trimmedLimit = limit.trim();
    if (!limitPattern.test(trimmedLimit)) {
      setError("初始额度最多两位小数。");
      return;
    }
    const limitNum = Number(trimmedLimit);
    if (!Number.isFinite(limitNum) || limitNum < LIMIT_MIN || limitNum > LIMIT_MAX) {
      setError(`初始额度必须在 $${LIMIT_MIN} - $${LIMIT_MAX} 之间。`);
      return;
    }
    const trimmedName = name.trim();
    const trimmedEmail = email.trim();
    const trimmedCountry = country.trim().toUpperCase();
    if (requireBilling) {
      if (!trimmedName) {
        setError("空卡池必须填写姓名。");
        return;
      }
      if (!emailPattern.test(trimmedEmail)) {
        setError("空卡池必须填写有效邮箱。");
        return;
      }
      if (!countryPattern.test(trimmedCountry)) {
        setError("空卡池必须填写国家 ISO 2 字母代码(如 US)。");
        return;
      }
    } else if (trimmedCountry && !countryPattern.test(trimmedCountry)) {
      setError("国家代码格式不正确(ISO 2 字母,如 US)。");
      return;
    } else if (trimmedEmail && !emailPattern.test(trimmedEmail)) {
      setError("邮箱格式不正确。");
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
      if (trimmedName) payload.billing_name = trimmedName;
      if (trimmedEmail) payload.email = trimmedEmail;
      if (trimmedCountry) payload.billing_country = trimmedCountry;
      if (postal.trim()) payload.billing_postal_code = postal.trim();
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
      maxWidth="sm"
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
            {requireBilling
              ? "首张卡必须完整填写姓名、邮箱、国家。"
              : "姓名、邮箱、国家留空会从已有卡自动继承。"}
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
              helperText={`必填,${LIMIT_MIN}-${LIMIT_MAX},最多两位小数。额度耗尽后自动跳过。`}
              required
              fullWidth
            />
            <TextField
              label={requireBilling ? "姓名(必填)" : "姓名(可选,留空继承)"}
              value={name}
              onChange={(e) => setName(e.target.value)}
              inputProps={{ autoComplete: "off" }}
              required={requireBilling}
              fullWidth
            />
            <Stack direction="row" spacing={2}>
              <TextField
                label={requireBilling ? "邮箱(必填)" : "邮箱(可选)"}
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                inputProps={{ autoComplete: "off" }}
                required={requireBilling}
                fullWidth
              />
              <TextField
                label={requireBilling ? "国家(必填 US/HK...)" : "国家(可选)"}
                value={country}
                onChange={(e) => setCountry(e.target.value.toUpperCase())}
                inputProps={{ maxLength: 2, autoComplete: "off" }}
                required={requireBilling}
                sx={{ maxWidth: 200 }}
                fullWidth
              />
            </Stack>
            <TextField
              label="邮编(可选)"
              value={postal}
              onChange={(e) => setPostal(e.target.value)}
              inputProps={{ autoComplete: "off" }}
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
