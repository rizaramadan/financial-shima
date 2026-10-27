-- name: SumMoneyFlowByWeek :many
-- Recap grain: one row per (currency, ISO-week bucket) summing
-- money_in pos_amount as inflow and money_out pos_amount as outflow.
-- Net (inflow - outflow) is computed in logic/balance (pure), not here.
-- Currency groups keep incommensurable units (idr vs gold-g) apart.
SELECT
    p.currency AS currency,
    date_trunc('week', t.effective_date)::date AS bucket,
    COALESCE(SUM(CASE WHEN t.type = 'money_in'  THEN t.pos_amount ELSE 0 END), 0)::bigint AS inflow,
    COALESCE(SUM(CASE WHEN t.type = 'money_out' THEN t.pos_amount ELSE 0 END), 0)::bigint AS outflow
FROM transactions t
JOIN pos p ON p.id = t.pos_id
WHERE t.type IN ('money_in', 'money_out')
  AND t.effective_date >= $1
  AND t.effective_date <= $2
GROUP BY p.currency, bucket
ORDER BY bucket DESC, p.currency;

-- name: SumMoneyFlowByMonth :many
-- Same shape as SumMoneyFlowByWeek at calendar-month grain. Reuses the
-- SumMoneyOutByPosMonth pattern (date_trunc + GROUP BY) extended with
-- the money_in side so each bucket carries in/out for the net.
SELECT
    p.currency AS currency,
    date_trunc('month', t.effective_date)::date AS bucket,
    COALESCE(SUM(CASE WHEN t.type = 'money_in'  THEN t.pos_amount ELSE 0 END), 0)::bigint AS inflow,
    COALESCE(SUM(CASE WHEN t.type = 'money_out' THEN t.pos_amount ELSE 0 END), 0)::bigint AS outflow
FROM transactions t
JOIN pos p ON p.id = t.pos_id
WHERE t.type IN ('money_in', 'money_out')
  AND t.effective_date >= $1
  AND t.effective_date <= $2
GROUP BY p.currency, bucket
ORDER BY bucket DESC, p.currency;

-- name: SumMoneyFlowByYear :many
-- Same shape as SumMoneyFlowByWeek at calendar-year grain.
SELECT
    p.currency AS currency,
    date_trunc('year', t.effective_date)::date AS bucket,
    COALESCE(SUM(CASE WHEN t.type = 'money_in'  THEN t.pos_amount ELSE 0 END), 0)::bigint AS inflow,
    COALESCE(SUM(CASE WHEN t.type = 'money_out' THEN t.pos_amount ELSE 0 END), 0)::bigint AS outflow
FROM transactions t
JOIN pos p ON p.id = t.pos_id
WHERE t.type IN ('money_in', 'money_out')
  AND t.effective_date >= $1
  AND t.effective_date <= $2
GROUP BY p.currency, bucket
ORDER BY bucket DESC, p.currency;
