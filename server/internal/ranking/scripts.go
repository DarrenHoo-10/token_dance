package ranking

import "github.com/redis/go-redis/v9"

const luaHelpers = `
local function rev_cmp(a, b)
  a = tostring(a or '0')
  b = tostring(b or '0')
  a = string.gsub(a, '^0+', '')
  b = string.gsub(b, '^0+', '')
  if a == '' then a = '0' end
  if b == '' then b = '0' end
  if #a ~= #b then
    if #a > #b then return 1 else return -1 end
  end
  if a > b then return 1 end
  if a < b then return -1 end
  return 0
end

local function parse_ver(raw)
  if not raw or raw == false then
    return '-1', false
  end
  raw = tostring(raw)
  local dpos = string.find(raw, '|D', 1, true)
  if dpos then
    return string.sub(raw, 1, dpos - 1), true
  end
  return raw, false
end

local function parse_payload(payload)
  if not payload or payload == false then
    return nil, nil, nil
  end
  payload = tostring(payload)
  local t1 = string.find(payload, '\t', 1, true)
  if not t1 then return nil, nil, nil end
  local t2 = string.find(payload, '\t', t1 + 1, true)
  if not t2 then return nil, nil, nil end
  return string.sub(payload, 1, t1 - 1), string.sub(payload, t1 + 1, t2 - 1), string.sub(payload, t2 + 1)
end

local function member_user(member)
  local last = nil
  local start = 1
  while true do
    local p = string.find(member, '|', start, true)
    if not p then break end
    last = p
    start = p + 1
  end
  if not last then return member end
  return string.sub(member, last + 1)
end
`

var applyScript = redis.NewScript(luaHelpers + `
-- KEYS: current, all, users, versions, meta, dirty
-- ARGV: generation, user_id, member, tokens, revision, op, now_ms, rule_version
local generation = ARGV[1]
local user_id = ARGV[2]
local member = ARGV[3]
local tokens = ARGV[4]
local revision = ARGV[5]
local op = ARGV[6]
local now_ms = ARGV[7]
local rule_version = ARGV[8]

local cur_gen = redis.call('HGET', KEYS[1], 'generation')
if cur_gen and cur_gen ~= false and cur_gen ~= '' and generation < cur_gen then
  return {'skipped_generation', cur_gen}
end

local future = false
if cur_gen and cur_gen ~= false and cur_gen ~= '' and generation > cur_gen then
  future = true
end

local ver_raw = redis.call('HGET', KEYS[4], user_id)
if ver_raw and ver_raw ~= false then
  local old_rev, tombstoned = parse_ver(ver_raw)
  if rev_cmp(revision, old_rev) < 0 then
    return {'stale', old_rev}
  end
  if rev_cmp(revision, old_rev) == 0 then
    if op == 'remove' then
      if tombstoned then
        return {'duplicate', old_rev}
      end
    else
      if tombstoned then
        return {'stale', old_rev}
      end
      return {'duplicate', old_rev}
    end
  end
end

local _, _, old_member = parse_payload(redis.call('HGET', KEYS[3], user_id))
if old_member then
  redis.call('ZREM', KEYS[2], old_member)
end

if op == 'remove' then
  redis.call('HDEL', KEYS[3], user_id)
  redis.call('HSET', KEYS[4], user_id, tostring(revision) .. '|D')
else
  redis.call('ZADD', KEYS[2], 0, member)
  redis.call('HSET', KEYS[3], user_id, tostring(revision) .. '\t' .. tokens .. '\t' .. member)
  redis.call('HSET', KEYS[4], user_id, tostring(revision))
end

redis.call('HINCRBY', KEYS[5], 'revision', 1)
redis.call('HSET', KEYS[5], 'appliedAt', now_ms, 'status', 'ready', 'generation', generation)

if (not cur_gen or cur_gen == false or cur_gen == '') and not future then
  redis.call('HSET', KEYS[1], 'generation', generation, 'ruleVersion', rule_version, 'status', 'ready')
end
if not future then
  redis.call('SET', KEYS[6], '1')
end

return {'applied', redis.call('HGET', KEYS[5], 'revision')}
`)

var promoteScript = redis.NewScript(`
-- KEYS: current, new_all, dirty
-- ARGV: generation, rule_version
local generation = ARGV[1]
local cur_gen = redis.call('HGET', KEYS[1], 'generation')
if cur_gen and cur_gen ~= false and cur_gen ~= '' then
  if generation < cur_gen then
    return 'stale'
  end
  if generation == cur_gen then
    return 'current'
  end
end
local n = redis.call('ZCARD', KEYS[2])
if n == 0 then
  return 'empty'
end
if cur_gen and cur_gen ~= false and cur_gen ~= '' then
  redis.call('HSET', KEYS[1], 'previousGeneration', cur_gen)
end
redis.call('HSET', KEYS[1], 'generation', generation, 'ruleVersion', ARGV[2], 'status', 'ready')
redis.call('SET', KEYS[3], '1')
return 'promoted'
`)

var captureScript = redis.NewScript(luaHelpers + `
-- KEYS: current, all, users, versions, meta, prev_users, prev_all
local gen = redis.call('HGET', KEYS[1], 'generation')
if not gen or gen == false or gen == '' then
  return {'empty'}
end
local rev = redis.call('HGET', KEYS[5], 'revision') or '0'
local members = redis.call('ZRANGE', KEYS[2], 0, tonumber(ARGV[1]) or 1999)
local count = redis.call('ZCARD', KEYS[2])
local rows = {tostring(gen), tostring(rev), tostring(count)}
for i = 1, #members do
  local member = members[i]
  local user_id = member_user(member)
  local _, tokens, _ = parse_payload(redis.call('HGET', KEYS[3], user_id))
  if not tokens then tokens = '0' end
  local prev_rank = ''
  local prev_payload = redis.call('HGET', KEYS[6], user_id)
  if prev_payload and prev_payload ~= false then
    local _, _, prev_member = parse_payload(prev_payload)
    if prev_member then
      local z = redis.call('ZRANK', KEYS[7], prev_member)
      if z ~= false then
        prev_rank = tostring(z + 1)
      end
    end
  end
  rows[#rows + 1] = user_id .. '\t' .. tokens .. '\t' .. prev_rank
end
return rows
`)

var publishScript = redis.NewScript(`
-- KEYS: hot_current, new_meta, new_rows
-- ARGV: snapshot_id, fence, generation, revision, expected_rows
local meta_ok = redis.call('EXISTS', KEYS[2])
if meta_ok == 0 then
  return 'missing'
end
local n = redis.call('LLEN', KEYS[3])
local expected = tonumber(ARGV[5]) or -1
if n ~= expected then
  return 'incomplete'
end
local cur_id = redis.call('GET', KEYS[1])
if cur_id and cur_id ~= false and cur_id ~= '' then
  local prefix_end = string.find(KEYS[1], ':hot:current', 1, true)
  local prefix = string.sub(KEYS[1], 1, prefix_end - 1)
  local old_meta = prefix .. ':hot:' .. cur_id .. ':meta'
  local old_gen, old_rev, old_fence = unpack(redis.call('HMGET', old_meta, 'generation', 'revision', 'fence'))
  if old_gen and old_gen ~= false and ARGV[3] < old_gen then
    return 'stale_gen'
  end
  if old_gen == ARGV[3] and old_rev and old_rev ~= false and tonumber(ARGV[4]) < tonumber(old_rev) then
    return 'stale_rev'
  end
  if old_fence and old_fence ~= false and tonumber(ARGV[2]) < tonumber(old_fence) then
    return 'stale_fence'
  end
end
redis.call('SET', KEYS[1], ARGV[1])
redis.call('HSET', KEYS[2], 'published', '1')
return 'ok'
`)

var readScript = redis.NewScript(luaHelpers + `
-- KEYS: current, hot_current
-- ARGV: snapshot_id, start, stop, user_id, public_cap
local prefix = string.sub(KEYS[1], 1, #KEYS[1] - 8)
local user_id = ARGV[4]
local start = tonumber(ARGV[2]) or 0
local stop = tonumber(ARGV[3]) or 0
local cap = tonumber(ARGV[5]) or 999
if start < 0 then start = 0 end
if stop < start then stop = start - 1 end
if start > cap then
  start = cap + 1
  stop = cap
end
if stop > cap then stop = cap end

local live_gen = redis.call('HGET', KEYS[1], 'generation')
if not live_gen or live_gen == false or live_gen == '' then
  return {'miss'}
end
local live_all = prefix .. ':g:' .. live_gen .. ':all'
local live_users = prefix .. ':g:' .. live_gen .. ':users'
local live_meta = prefix .. ':g:' .. live_gen .. ':meta'
local live_rev = redis.call('HGET', live_meta, 'revision') or '0'
local prev_gen = redis.call('HGET', KEYS[1], 'previousGeneration')
local prev_users = nil
local prev_all = nil
if prev_gen and prev_gen ~= false and prev_gen ~= '' then
  prev_users = prefix .. ':g:' .. prev_gen .. ':users'
  prev_all = prefix .. ':g:' .. prev_gen .. ':all'
end

local function prev_rank_for(uid)
  if not prev_users then return '' end
  local prev_payload = redis.call('HGET', prev_users, uid)
  if not prev_payload or prev_payload == false then return '' end
  local _, _, prev_member = parse_payload(prev_payload)
  if not prev_member then return '' end
  local z = redis.call('ZRANK', prev_all, prev_member)
  if z == false then return '' end
  return tostring(z + 1)
end

local function own_fields()
  if user_id == '' then
    return '', ''
  end
  local payload = redis.call('HGET', live_users, user_id)
  if not payload or payload == false then
    return '', ''
  end
  local _, tokens, member = parse_payload(payload)
  if not member then
    return '', tostring(tokens or '0')
  end
  local z = redis.call('ZRANK', live_all, member)
  if z == false then
    return '', tostring(tokens or '0')
  end
  return tostring(z + 1), tostring(tokens or '0')
end

local function row_from_member(member)
  local uid = member_user(member)
  local payload = redis.call('HGET', live_users, uid)
  local tokens = '0'
  if payload and payload ~= false then
    local _, t, _ = parse_payload(payload)
    if t then tokens = t end
  end
  return uid .. '\t' .. tokens .. '\t' .. prev_rank_for(uid)
end

local snap_id = ARGV[1]
if snap_id == '' then
  local cur = redis.call('GET', KEYS[2])
  if cur and cur ~= false then
    snap_id = tostring(cur)
  end
end

local hot_meta = prefix .. ':hot:' .. snap_id .. ':meta'
local hot_rows = prefix .. ':hot:' .. snap_id .. ':rows'
local hot_gen = redis.call('HGET', hot_meta, 'generation')
local hot_rev = redis.call('HGET', hot_meta, 'revision')
local matched = (snap_id ~= '' and hot_gen == live_gen and tostring(hot_rev or '') == tostring(live_rev or ''))

if user_id ~= '' and not matched then
  local page = redis.call('ZRANGE', live_all, start, stop)
  local count = redis.call('ZCARD', live_all)
  local own_rank, own_tokens = own_fields()
  local out = {'live', snap_id, tostring(live_gen), tostring(live_rev), tostring(count), own_rank, own_tokens}
  for i = 1, #page do
    out[#out + 1] = row_from_member(page[i])
  end
  return out
end

if snap_id == '' or not hot_gen or hot_gen == false then
  return {'miss'}
end
local page = redis.call('LRANGE', hot_rows, start, stop)
local count = redis.call('HGET', hot_meta, 'participants') or '0'
local own_rank, own_tokens = '', ''
if user_id ~= '' then
  own_rank, own_tokens = own_fields()
end
local out = {'hot', snap_id, tostring(hot_gen), tostring(hot_rev or '0'), tostring(count), own_rank, own_tokens}
for i = 1, #page do
  out[#out + 1] = page[i]
end
return out
`)
