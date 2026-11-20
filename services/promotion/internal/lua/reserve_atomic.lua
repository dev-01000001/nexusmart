-- reserve_atomic.lua
-- Atomically validates and reserves flash-sale slots and voucher quotas/budgets
-- in a single all-or-nothing Redis execution, idempotent by order_id.
--
-- KEYS layout:
--   KEYS[1] = promo:res:<order_id>
--   For each flash item i in 1..F:
--     KEYS[1 + (i-1)*2 + 1] = promo:flash:<item_id>:remaining
--     KEYS[1 + (i-1)*2 + 2] = promo:flash:<item_id>:user:<user_id>
--   For each voucher j in 1..V:
--     base = 1 + F*2 + (j-1)*3
--     KEYS[base + 1] = promo:voucher:<voucher_id>:remaining
--     KEYS[base + 2] = promo:voucher:<voucher_id>:user:<user_id>
--     KEYS[base + 3] = promo:voucher:<voucher_id>:budget_remaining
--
-- ARGV layout:
--   ARGV[1] = F (number of flash sale items)
--   ARGV[2] = V (number of vouchers)
--   ARGV[3] = ttl_seconds for reservation key (0 = no TTL)
--   For each flash item i in 1..F:
--     ARGV[3 + (i-1)*2 + 1] = qty
--     ARGV[3 + (i-1)*2 + 2] = per_user_limit
--   For each voucher j in 1..V:
--     base = 3 + F*2 + (j-1)*3
--     ARGV[base + 1] = per_user_limit
--     ARGV[base + 2] = discount_vnd
--     ARGV[base + 3] = has_budget (1 or 0)

local resKey = KEYS[1]
local numFlash = tonumber(ARGV[1]) or 0
local numVouchers = tonumber(ARGV[2]) or 0
local ttlSeconds = tonumber(ARGV[3]) or 0

-- Idempotency check
local existing = redis.call("GET", resKey)
if existing then
  if existing == "RELEASED" then
    return {"ERR", "ORDER_ALREADY_RELEASED", resKey}
  end
  if string.sub(existing, 1, 8) == "RESERVED" then
    return {"ALREADY_RESERVED"}
  end
end

-- Phase 1: Validate ALL flash sale items BEFORE mutating any key
for i = 1, numFlash do
  local keyBase = 1 + (i - 1) * 2
  local argBase = 3 + (i - 1) * 2
  local remKey = KEYS[keyBase + 1]
  local userKey = KEYS[keyBase + 2]
  local qty = tonumber(ARGV[argBase + 1])
  local userLimit = tonumber(ARGV[argBase + 2])

  if not qty or qty <= 0 then
    return {"ERR", "INVALID_FLASH_QTY", remKey}
  end

  local remStr = redis.call("GET", remKey)
  if not remStr then
    return {"ERR", "FLASH_NOT_INITIALIZED", remKey}
  end
  local rem = tonumber(remStr)
  if not rem or rem < qty then
    return {"ERR", "FLASH_SOLD_OUT", remKey}
  end

  local usedStr = redis.call("GET", userKey)
  local used = tonumber(usedStr or "0") or 0
  if userLimit and userLimit > 0 and (used + qty) > userLimit then
    return {"ERR", "FLASH_USER_LIMIT_EXCEEDED", userKey}
  end
end

-- Phase 1: Validate ALL vouchers BEFORE mutating any key
for j = 1, numVouchers do
  local keyBase = 1 + numFlash * 2 + (j - 1) * 3
  local argBase = 3 + numFlash * 2 + (j - 1) * 3
  local remKey = KEYS[keyBase + 1]
  local userKey = KEYS[keyBase + 2]
  local budgetKey = KEYS[keyBase + 3]
  local userLimit = tonumber(ARGV[argBase + 1])
  local discountVND = tonumber(ARGV[argBase + 2]) or 0
  local hasBudget = tonumber(ARGV[argBase + 3]) or 0

  local remStr = redis.call("GET", remKey)
  if not remStr then
    return {"ERR", "VOUCHER_NOT_INITIALIZED", remKey}
  end
  local rem = tonumber(remStr)
  if not rem or rem < 1 then
    return {"ERR", "VOUCHER_OUT_OF_QUOTA", remKey}
  end

  local usedStr = redis.call("GET", userKey)
  local used = tonumber(usedStr or "0") or 0
  if userLimit and userLimit > 0 and (used + 1) > userLimit then
    return {"ERR", "VOUCHER_USER_LIMIT_EXCEEDED", userKey}
  end

  if hasBudget == 1 then
    local bStr = redis.call("GET", budgetKey)
    if not bStr then
      return {"ERR", "VOUCHER_BUDGET_NOT_INITIALIZED", budgetKey}
    end
    local budget = tonumber(bStr)
    if not budget or budget < discountVND then
      return {"ERR", "VOUCHER_BUDGET_EXHAUSTED", budgetKey}
    end
  end
end

-- Phase 2: All checks passed — mutate ALL keys atomically and persist rollback payload
local lines = {"RESERVED"}

for i = 1, numFlash do
  local keyBase = 1 + (i - 1) * 2
  local argBase = 3 + (i - 1) * 2
  local remKey = KEYS[keyBase + 1]
  local userKey = KEYS[keyBase + 2]
  local qty = tonumber(ARGV[argBase + 1])

  redis.call("DECRBY", remKey, qty)
  redis.call("INCRBY", userKey, qty)
  table.insert(lines, "F|" .. remKey .. "|" .. userKey .. "|" .. tostring(qty))
end

for j = 1, numVouchers do
  local keyBase = 1 + numFlash * 2 + (j - 1) * 3
  local argBase = 3 + numFlash * 2 + (j - 1) * 3
  local remKey = KEYS[keyBase + 1]
  local userKey = KEYS[keyBase + 2]
  local budgetKey = KEYS[keyBase + 3]
  local discountVND = tonumber(ARGV[argBase + 2]) or 0
  local hasBudget = tonumber(ARGV[argBase + 3]) or 0

  redis.call("DECR", remKey)
  redis.call("INCR", userKey)
  if hasBudget == 1 and discountVND > 0 then
    redis.call("DECRBY", budgetKey, discountVND)
  end
  table.insert(lines, "V|" .. remKey .. "|" .. userKey .. "|" .. budgetKey .. "|" .. tostring(discountVND) .. "|" .. tostring(hasBudget))
end

local payload = table.concat(lines, "\n")
if ttlSeconds and ttlSeconds > 0 then
  redis.call("SET", resKey, payload, "EX", ttlSeconds)
else
  redis.call("SET", resKey, payload)
end

return {"RESERVED"}
