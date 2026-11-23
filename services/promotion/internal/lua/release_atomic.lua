-- release_atomic.lua
-- Strictly idempotent atomic compensation for a reserved promotion by order_id.
-- Backward-compatible with legacy reservations containing only F| and V| lines.
--
-- KEYS[1] = promo:res:<order_id>
-- ARGV[1] = ttl_seconds for RELEASED tombstone (0 = no TTL)

local resKey = KEYS[1]
local ttlSeconds = tonumber(ARGV[1]) or 0

local val = redis.call("GET", resKey)
if not val then
  if ttlSeconds and ttlSeconds > 0 then
    redis.call("SET", resKey, "RELEASED", "EX", ttlSeconds)
  else
    redis.call("SET", resKey, "RELEASED")
  end
  return {"ALREADY_RELEASED"}
end
if val == "RELEASED" then
  return {"ALREADY_RELEASED"}
end

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

    if parts[1] == "F" and #parts >= 4 then
      local remKey = parts[2]
      local userKey = parts[3]
      local qty = tonumber(parts[4]) or 0
      if qty > 0 then
        redis.call("INCRBY", remKey, qty)
        local newUsed = redis.call("DECRBY", userKey, qty)
        if newUsed <= 0 then
          redis.call("DEL", userKey)
        end
      end
    elseif parts[1] == "V" and #parts >= 6 then
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
    elseif parts[1] == "P" and #parts >= 3 then
      local userKey = parts[2]
      local qty = tonumber(parts[3]) or 0
      if qty > 0 then
        local newUsed = redis.call("DECRBY", userKey, qty)
        if newUsed <= 0 then
          redis.call("DEL", userKey)
        end
      end
    end
  end
end

if ttlSeconds and ttlSeconds > 0 then
  redis.call("SET", resKey, "RELEASED", "EX", ttlSeconds)
else
  redis.call("SET", resKey, "RELEASED")
end

return {"RELEASED"}
