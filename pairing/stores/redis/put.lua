-- Put(nonce, ttl_ms): create a Waiting entry if none exists.
--
-- KEYS[1] = pairing:<nonce>
-- ARGV[1] = TTL in milliseconds (string)
--
-- Returns:
--   1  entry created
--   0  entry already exists (ErrAlreadyExists)

if redis.call('EXISTS', KEYS[1]) == 1 then
    return 0
end
redis.call('HSET', KEYS[1], 'status', 'waiting')
redis.call('PEXPIRE', KEYS[1], ARGV[1])
return 1
