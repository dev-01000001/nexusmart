-- adjust_voucher.lua
-- Atomically synchronizes a voucher's status, remaining quota delta, and remaining budget delta
-- without clobbering concurrent reserve_atomic.lua decrements.
--
-- KEYS[1] = promo:voucher:<id>:remaining
-- KEYS[2] = promo:voucher:<id>:budget_remaining
-- KEYS[3] = promo:voucher:<id>:status
--
-- ARGV[1] = status ("ACTIVE" | "PAUSED" | "DELETED")
-- ARGV[2] = quota_delta (newQuota - oldQuota)
-- ARGV[3] = fallback_rem_quota (used if KEYS[1] does not exist yet)
-- ARGV[4] = old_budget_vnd
-- ARGV[5] = new_budget_vnd
-- ARGV[6] = fallback_rem_budget (used if KEYS[2] does not exist yet or old_budget_vnd == 0)

local remKey = KEYS[1]
local budgetKey = KEYS[2]
local statusKey = KEYS[3]

local statusVal = ARGV[1]
local quotaDelta = tonumber(ARGV[2]) or 0
local fallbackRemQuota = tonumber(ARGV[3]) or 0
local oldBudget = tonumber(ARGV[4]) or 0
local newBudget = tonumber(ARGV[5]) or 0
local fallbackRemBudget = tonumber(ARGV[6]) or 0

if statusVal and statusVal ~= "" then
  redis.call("SET", statusKey, statusVal)
end

local curRemStr = redis.call("GET", remKey)
if not curRemStr then
  if fallbackRemQuota < 0 then
    fallbackRemQuota = 0
  end
  redis.call("SET", remKey, fallbackRemQuota)
elseif quotaDelta ~= 0 then
  -- Do NOT clamp negative deficits to 0: reserve_atomic.lua treats rem < 1 as exhausted,
  -- and preserving negative values prevents releases from reviving over-allocated quota.
  redis.call("INCRBY", remKey, quotaDelta)
end

if newBudget <= 0 then
  redis.call("DEL", budgetKey)
elseif oldBudget <= 0 then
  if fallbackRemBudget < 0 then
    fallbackRemBudget = 0
  end
  redis.call("SET", budgetKey, fallbackRemBudget)
else
  local budgetDelta = newBudget - oldBudget
  local curBudStr = redis.call("GET", budgetKey)
  if not curBudStr then
    if fallbackRemBudget < 0 then
      fallbackRemBudget = 0
    end
    redis.call("SET", budgetKey, fallbackRemBudget)
  elseif budgetDelta ~= 0 then
    -- Preserve negative budget deficits so subsequent releases cannot revive over-allocated budget.
    redis.call("INCRBY", budgetKey, budgetDelta)
  end
end

return {"OK"}
