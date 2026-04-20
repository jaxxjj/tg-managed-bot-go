-- FetchAndDelete(nonce): atomically read a Ready entry and delete it.
--
-- KEYS[1] = pairing:<nonce>
--
-- Returns:
--   nil                                  ErrNotFound (never Put or expired)
--   -1                                   ErrNotReady (still Waiting)
--   {token, bot_username, completed_at}  success — entry deleted
--
-- Atomicity: under Redis' single-threaded Lua execution, the HGETs and
-- the DEL are one logical step. Concurrent callers see exactly one
-- success; all others see nil (ErrNotFound) on their next call.

local status = redis.call('HGET', KEYS[1], 'status')
if not status then
    return nil
end
if status ~= 'ready' then
    return -1
end
local token = redis.call('HGET', KEYS[1], 'token')
local bot_username = redis.call('HGET', KEYS[1], 'bot_username')
local completed_at = redis.call('HGET', KEYS[1], 'completed_at')
redis.call('DEL', KEYS[1])
return {token, bot_username, completed_at}
