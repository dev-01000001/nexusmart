-- release_partial_atomic.lua
-- Idempotently releases a subset of reserved flash-sale items and vouchers from promo:res:<order_id>,
-- keeping unreleased entries intact for later partial releases or full release_atomic.lua.
--
-- KEYS[1] = promo:res:<order_id>
-- KEYS[2..] = optional touched keys (for Redis Cluster compatibility)
-- ARGV[1] = ttl_seconds for updated reservation payload (0 = no TTL)
-- ARGV[2..] = target remaining-counter keys to release (e.g. promo:flash:<id>:remaining, promo:voucher:<id>:remaining)

local resKey = KEYS[1]
local ttlSeconds = tonumber(ARGV[1]) or 0

local val = redis.call("GET", resKey)
if not val or val == "RELEASED" then
  return {"ALREADY_RELEASED"}
end

local targets = {}
local targetCount = 0
for i = 2, #ARGV do
  targets[ARGV[i]] = true
  targetCount = targetCount + 1
end

if targetCount == 0 then
  return {"ALREADY_RELEASED"}
end

local remainingLines = {"RESERVED"}
local releasedAny = false
local isFirst = true

for line in string.gmatch(val, "[^\n]+") do
  if isFirst then
    isFirst = false
    if line ~= "RESERVED" then
      return {"ALREADY_RELEASED"}
    end
  else
    local parts = {}
    for part in string.gmatch(line, "[^|]+") do
      table.insert(parts, part)
    end

    if parts[1] == "F" and #parts >= 4 and targets[parts[2]] then
      local remKey = parts[2]
      local userKey = parts[3]
      local qty = tonumber(parts[4]) or 0
      if qty > 0 then
        redis.call("INCRBY", remKey, qty)
        local newUsed = redis.call("DECRBY", userKey, qty)
        if newUsed <= 0 then
          redis.call("DEL", userKey)
        end
        releasedAny = true
      end
    elseif parts[1] == "V" and #parts >= 6 and targets[parts[2]] then
      local remKey = parts[2]
      local userKey = parts[3]
      local budgetKey = parts[4]
      local discountVND = tonumber(parts[5]) or 0
      local hasBudget = tonumber(parts[6]) or 0

      redis.call("INCR", remKey)
      local newUsed = redis.call("DECR", userKey)
      if newUsed <= 0 then
        redis.call("DEL", userKey)
      end
      if hasBudget == 1 and discountVND > 0 then
        redis.call("INCRBY", budgetKey, discountVND)
      end
      releasedAny = true
    else
      table.insert(remainingLines, line)
    end
  end
end

if not releasedAny then
  return {"ALREADY_RELEASED"}
end

local payload = table.concat(remainingLines, "\n")
if ttlSeconds and ttlSeconds > 0 then
  redis.call("SET", resKey, payload, "EX", ttlSeconds)
else
  redis.call("SET", resKey, payload)
end

return {"RELEASED"}
