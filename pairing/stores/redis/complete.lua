-- Complete(nonce, token, bot_username, completed_at_unix)
-- Transitions a Waiting entry to Ready.
--
-- KEYS[1] = pairing:<nonce>
-- ARGV[1] = token
-- ARGV[2] = bot_username
-- ARGV[3] = completed_at Unix seconds (string)
--
-- Returns:
--    1  ok
--   -1  ErrNotFound (nonce never existed or expired)
--   -2  ErrInvalidState (entry not Waiting — already Ready/other)
--
-- TTL is preserved: HSET does not touch it, and we rely on the PEXPIRE
-- set in put.lua so stale Ready entries eventually reap themselves.

local status = redis.call('HGET', KEYS[1], 'status')
if not status then
    return -1
end
if status ~= 'waiting' then
    return -2
end
redis.call('HSET', KEYS[1],
    'status', 'ready',
    'token', ARGV[1],
    'bot_username', ARGV[2],
    'completed_at', ARGV[3])
return 1
