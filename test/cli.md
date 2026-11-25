
## Server

### Launch
```
./bin/rate-limiter -config ./etc/config.yaml
```

## Redis CLI

### Get the rule hash
```
hget rl:cfg:/api/v1/order/pay:client_001:192.168.11.2 rate

hget rl:cfg:/api/v1/order/pay:client_001:192.168.11.2 burst
```

### List the members in redis key index
```
SMEMBERS rl:cfg:index
```

## Client API Invoke
### Send mock request
```
python3 test/python_req.py --rate=10 --host=127.0.0.1:18099 --count=20
python3 test/python_req.py --case=2 --client_id=client_id_001 --ip=127.0.0.1
```