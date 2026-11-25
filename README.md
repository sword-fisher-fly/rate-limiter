# Distributed cluster rate-limiting plugin

## Table of content

- [Introduction](#introduction)
- [Architecture](#architecture)
- [Core features](#core-features)
  - [Rate Limit Configuration](#rate-limit-configuration)
  - [Token Bucket vs Fixed window](#token-bucket-vs-fixed-window)
  - [Rule Priority](#rule-priority)
  - [Overlapping Rules](#overlapping-rules)
  - [Failover and Compensation Mechanism](#failover-and-compensation-mechanism)
- [API](#api)
  - [Rate limit configuration](#rate-limit-configuration-1)
  - [Common Business API](#common-business-api)
- [Test](#test)
- [Build & Launch & Test](#build--launch--test)
  - [Build as a binary](#build-as-a-binary)
  - [Build as an image](#build-as-an-image)
  - [Launch the server instance](#launch-the-server-instance)
  - [Mock test](#mock-test)
    - [Set the rate limiter](#set-the-rate-limiter)
    - [Send mock business API requests concurrently](#send-mock-business-api-requests-concurrently)
- [TODO List](#todo-list)

## Introduction

Build a global traffic scheduling middleware that supports multi-instance deployment and is driven by external shared storage.
Under a multi-replica gateway architecture (with ≥ 3 replicas), traditional single-node in-memory rate limiting cannot enforce quotas precisely because the quota is fragmented across replicas. A Redis-based cluster solution is required to ensure that N nodes reach strong-consistency consensus within the same quota pool.


## Architecture

```Production Design 
┌─────────────────────────────────────────────────────────────────────────────┐
│                              CLIENT LAYER                                    │
│                                                                              │
│   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐   ┌──────────────┐ │
│   │  User Client │   │  User Client │   │  User Client │   │  User Client │ │
│   │   (Web/App)  │   │   (Mobile)   │   │   (API)      │   │   (SDK)      │ │
│   └──────┬───────┘   └──────┬───────┘   └──────┬───────┘   └──────┬───────┘ │
└──────────┼──────────────────┼──────────────────┼──────────────────┼─────────┘
           │                  │                  │                  │
           └──────────────────┴─────┬────────────┴──────────────────┘
                                    │  HTTPS Requests
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                         LOAD BALANCER / API GATEWAY                          │
│                    (TLS termination, routing, auth token check)              │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                  RATE LIMITER PLUGIN (Gateway Plugin)               │   │
│   │   • Intercepts request BEFORE routing                               │   │
│   │   • Calls Redis with atomic Lua scripts / EVALSHA                   │   │
│   │   • ALLOW  → forward to real server instance                        │   │
│   │   • DENY   → return 429 immediately (no server hop)                 │   │
│   └───────┬─────────────────────────────────────────────┬───────────────┘   │
└───────────┼─────────────────────────────────────────────┼───────────────────┘
            │                                             │
            │  (if rate limit allowed)                    │  (config / metrics /
            │                                             │   overrides / state)
            ▼                                             ▼
┌───────────────────────────┐             ┌─────────────────────────────────────┐
│    SERVER INSTANCES       │             │   MIDDLEWARE / INFRA LAYER          │
│  (Business Logic only)    │             │   (Backend service of API Gateway)  │
│                           │             │                                     │
│ ┌───────────┐             │             │ ┌─────────────────────────────────┐ │
│ │ Business  │             │             │ │  REDIS CLUSTER (Rate Limit Store)│ │
│ │  Logic    │             │             │ │                                 │ │
│ └───────────┘             │             │ │  Master 1   Master 2   Master 3 │ │
│ ┌───────────┐             │             │ │  (slots)    (slots)    (slots)  │ │
│ │ Business  │             │             │ │     │          │          │     │ │
│ │  Logic    │             │             │ │  Replica 1 Replica 2 Replica 3  │ │
│ └───────────┘             │             │ │  (failover)                     │ │
│ ┌───────────┐             │             │ │                                 │ │
│ │ Business  │             │             │ │  Data model: rt:{client_id}:{ctr}│ │
│ │  Logic    │             │             │ │   • Token bucket (TTL-based)    │ │
│ └───────────┘             │             │ │   • Lua: INCR + EXPIRE (atomic) │ │
│                           │             │ └─────────────────────────────────┘ │
│  Inst 1    Inst 2   Inst N│             │                                     │
└───────────────────────────┘             │ ┌──────────────┐ ┌──────────────┐   │
                                          │ │Config Service│ │ Metrics/Logs │   │
                                          │ │(limits,rules)│ │ (Prometheus) │   │
                                          │ └──────────────┘ └──────────────┘   │
                                          │ ┌──────────────┐                    │
                                          │ │Admin Dashboard│                   │
                                          │ │ (override)   │                    │
                                          │ └──────────────┘                    │
                                          └─────────────────────────────────────┘
```
***Layer 1: Client Layer***
The entry point of all traffic. It represents diverse consumers—web/app users, mobile clients, direct API callers, and SDK integrations—each sending HTTPS requests into the system. This layer is where the demand originates; the rest of the architecture exists to serve it safely and fairly.

***Layer 2:Load Balancer / API Gateway***
The edge tier that terminates TLS, routes requests, and validates auth tokens. In this design it also hosts the Rate Limiter Plugin, which intercepts every request before routing, consults Redis via atomic Lua scripts, and either forwards the request (ALLOW) to a server instance or rejects it immediately with 429 (DENY). This means abusive or over-quota traffic is shed at the edge and never consumes backend capacity.


***Layer 3-1: Middleware / Infra Layer***
This tier is positioned as the backend service of the API Gateway, not a shared tier behind the servers. It consists of:

***Redis Cluster (Rate Limit Store)***: sharded by ClientID/IP key across masters with replicas for failover. Keys follow ratelimit:{user_id}:{endpoint}, storing token-bucket or fix-window counters with TTL. Atomicity is guaranteed via Lua scripts (INCR + EXPIRE, EVALSHA).

***Config Service***: manages limits and rules centrally.

***Admin Dashboard***: allows manual overrides.

***Metrics / Logs***: monitor the behavior of the rate limiter and server state. It is optional in the design and implemention.

***Layer 3-2:Server Instances (1…N)***
Horizontally scaled application servers that hold only Business Logic. They receive requests that have already passed the gateway's rate-limit check. Keeping them free of rate-limiting concerns lets them scale independently and stay focused on core processing.

***Real practice as Demo***
To implement and test the end-to-end rate limiter in a local environment, we relocate the rate limiter plugin into the server instances as built-in middleware. Although it operates at a different layer, it is functionally equivalent in principle.
```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              CLIENT LAYER                                    │
│                                                                              │
│                    ┌──────────────────────────────┐                          │
│                    │        User Client (API)     │                          │
│                    └──────────────┬───────────────┘                          │
└───────────────────────────────────┼─────────────────────────────────────────┘
                                    │  HTTPS Requests
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                    SERVER INSTANCE (Mock / Single Node)                      │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │                          Business Logic                             │   │
│   └───────────────────────────────┬─────────────────────────────────────┘   │
│                                   │                                         │
│   ┌───────────────────────────────▼─────────────────────────────────────┐   │
│   │                    RATE LIMITER MIDDLEWARE                          │   │
│   │   • Intercepts request before business logic                        │   │
│   │   • Calls Redis with atomic Lua scripts / EVALSHA                   │   │
│   │   • ALLOW  → proceed to business logic                              │   │
│   │   • DENY   → return 429 immediately and Retry-After if needed       │   │
│   └───────────────────────────────┬─────────────────────────────────────┘   │
└───────────────────────────────────┼─────────────────────────────────────────┘
                                    │
                                    │   Redis protocol (atomic Lua scripts / EVALSHA)
                                    ▼
┌─────────────────────────────────────────────────────────────────────────────┐
│                          MIDDLEWARE / INFRA LAYER                            │
│                                                                              │
│   ┌─────────────────────────────────────────────────────────────────────┐   │
│   │               REDIS INSTANCE (Rate Limit Store)                     │   │
│   │                                                                      │   │
│   │   ┌──────────────────────────────────────────────────────────────┐  │   │
│   │   │                      Single Redis Node                        │  │   │
│   │   │   • Shared quota pool (single instance for mock)             │  │   │
│   │   │   • Atomic Lua scripts (INCR + EXPIRE / EVALSHA)             │  │   │
│   │   │   • TTL-based token bucket counters                          │  │   │
│   │   └──────────────────────────────────────────────────────────────┘  │   │
│   │                                                                      │   │
│   │   Data model per key:  rt:cfg:{path}:{client_id}:{ip}                 │   │
│   └─────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
│   ┌──────────────────┐   ┌──────────────────┐   ┌──────────────────┐        │
│   │  Config Service  │   │  Metrics / Logs  │   │  Admin Dashboard │        │
│   │  (limits, rules) │   │ (Prometheus etc) │   │  (override)      │        │
│   └──────────────────┘   └──────────────────┘   └──────────────────┘        │
└─────────────────────────────────────────────────────────────────────────────┘
```


***Big difference: Server Instances (1…N)***
Horizontally scaled business-logic servers. Each instance embeds a Rate Limiter Middleware that, on every request, consults the shared Redis store. Because limits must be enforced globally across all instances, the middleware cannot rely on local in-memory counters—it must coordinate through the external store.

## Core features

### Rate Limit Configuration
The implementation uses the RequestPath, ClientID, and IP fields to configure the rate limiter plugin's rule. RequestPath is required, while ClientID and IP are optional. The rate field specifies the rate at which tokens are generated per second. The burst field specifies the maximum number of tokens allowed at startup.

An example looks like as follows.
```
{
    "name": "rate limiter mock",
    "path": "/api/v1/rate-limiter-mock",
    "ip": "127.0.0.1",
    "rate": 5,
    "burst": 5
}
```

### Token Bucket vs Fixed window

| Dimension | Token Bucket | Fixed Window |
|---|---|---|
| Traffic shape | The tokens are added continuously at `rate` per second, so the allowed traffic is spread evenly | The whole quota of a window is released at its start, so the traffic arrives in bursts |
| Window boundary | No boundary effect, the quota of a period is never exceeded | Up to twice the quota can pass around a boundary, the quota of the last window plus the quota of the next one |
| Burst control | The bucket holds at most `burst` tokens, so the instantaneous load is capped | The concurrency is not capped, the whole quota of the window can be served at once |
| `Retry-After` | Exact, the script reports the seconds until the bucket holds a token again | Imprecise, the client has to wait for the end of the window, which can be much longer than needed |
| State per key | Two numbers, `tokens` and `last_refill`, in a hash | A single counter |
| Atomic operation | A Lua script that refills the bucket and takes a token in one round trip | `INCR` and `EXPIRE` |
| Clock dependency | The refill depends on the elapsed time measured by the instance, so the clocks of the replicas have to be in sync | Counters only, no clock dependency |

***Why we use the Token Bucket strategy***
The rule of a config is a `rate` (the tokens generated per second) and a `burst` (the capacity of the bucket), which is the model of a token bucket itself. A fixed window would need an extra window length field and would change the rule model of the plugin.

The plugin sheds the over-quota traffic at the edge, before it reaches the business instances, so the traffic that is let through has to be spread evenly to protect the backend. A fixed window releases the whole quota at once, which would let a burst of traffic through and defeat the purpose of the plugin.

The bucket also reports the exact number of seconds until the next request is allowed, so the 429 response carries a `Retry-After` header the client can act on.

The bucket is stored in Redis and every instance of the service takes a token from the same one, so the quota stays global across the replicas.


### Rule Priority
We apply an "all rules must allow" check: the request is permitted only if every matching rule allows it. If no rule constrains the request, the request is allowed by default. The example as belows

***Constraint***
Note: path field is required, client_id and ip are optional.

```
1. rt:cfg:{path}:{client_id}:{ip}      ← most specific
2. rt:cfg:{path}:{client_id}:__any__
3. rt:cfg:{path}:__any__:{ip}
4. rt:cfg:{path}:__any__:__any__       ← least specific (catch-all)
```

### Overlapping Rules
Key points for the overlap scenario

Layered (all matches must pass)
Every matching rule is enforced independently, and the request is allowed only if all buckets allow it. The specific rule and the wildcard rule each consume from their own bucket; a request is denied if either is empty.

### Failover and Compensation Mechanism
When Redis is unavailable, Allow returns an error. The caller must decide between fail-open (allow) and fail-closed (reject). For most API gateway scenarios, fail-open with alerting is recommended, so a limiter component failure doesn't take the entire site down. 

## API 

### Rate limit configuration 

Configure the rate limiter rule for the specified API. A mock API rule has been created as follows.
```
POST /api/v1/configs
{
    "name": "rate limiter mock",
    "path": "/api/v1/rate-limiter-mock",
    "rate": 1,
    "burst": 1
}
```


### Common Business API
I use the `/api/v1/rate-limiter-mock` as a business API to check the effectiveness of the rate limit plugin.

## Test

In the directory of `test`, I write a http client to sending http request to the server in python. In addition, create the rate limit configure via RESTful API plugin in the file `test/rest.http`


## Build & Launch & Test

### Build as a binary
```
make build
```

### Build as an image

```
make build-image
```

### Launch the server instance 
```
./bin/rate-limiter -config ./etc/config.yaml
```


### Mock test

#### Set the rate limiter
There are a few examples located in the file `test/rest.http`.

1) only with path
```
{
    "name": "rate limiter mock",
    "path": "/api/v1/rate-limiter-mock",
    "rate": 1,
    "burst": 1
}
```
2) with client id and without ip
```
{
    "name": "rate limiter mock",
    "path": "/api/v1/rate-limiter-mock",
    "client_id": "client_001",
    "rate": 1,
    "burst": 1
}
```

3) with client id and with ip
```
{
    "name": "rate limiter mock",
    "path": "/api/v1/rate-limiter-mock",
    "client_id": "client_001",
    "ip": "127.0.0.1",
    "rate": 1,
    "burst": 1
}
```

4) without client id and with ip
```
{
    "name": "rate limiter mock",
    "path": "/api/v1/rate-limiter-mock",
    "ip": "127.0.0.1",
    "rate": 1,
    "burst": 1
}
```

#### Send mock business API requests concurrently

See the detail in the file `test/python_req.py`.

```
python3 test/python_req.py --rate=10 --host=127.0.0.1:18099 --count=20 --client_id=client_id_001 --case=1
```

| Argument | Description |
| --- | --- |
| `--rate` | the number of the requests sent per second |
| `--host` | the address of the rate limiter service |
| `--count` | the total number of the requests to send |
| `--client_id` | the client id sent in the `X-Client-Id` header |
| `--ip` | the ip sent in the `X-Forwarded-For` header |
| `--case` | the scenario to mock, see the table below |

The case selects the rate limit config the requests match:

| Case | Scenario | Headers |
| --- | --- | --- |
| 1 | only the path | none |
| 2 | the client id and the ip | `X-Client-Id`, `X-Forwarded-For` |
| 3 | the client id, no ip | `X-Client-Id` |
| 4 | the ip, no client id | `X-Forwarded-For` |

The result of every request is printed, a request rejected by the rate limiter
is printed in red with the number of the seconds it has to wait, the client
waits for it and sends the same request again.

```
[Thread 1] [1] [OK]
[Thread 2] [3] [FAIL] [Try After 1s]
```

The first number is the worker thread that sent the request, the second one is
the number of the request.


## TODO List

- [] Add `metric` and `log` to monitor the behavior the API Gateway and rate limiter plugin.
- [] Add a web dashboard for the plugin that lets users visibly configure rate limits by specifying parameters such as `ClientID`, `IP`, time window and traffic threshold.
- [] Add the startup sync to republish the configs 
- [] Peridically compare the rate limit configure in redis and database, if unconsistent, then override the configure in the redis with the newest one stored in the database
- [] Add rate limit configure constraint when performing write operation on the specified rule that is overrided with the wild rule with the same path
- [] Add path rule with wildcard pattern, such as `/api/v1/order/(+\d)`
- [] Whether it needs to add request method as a rule field to rate limit the API
- [] Add web portal to create, update and display the rate limit configure
- [] Add fail-open mechinism to enhance the high availability of rate limit plugin  
- [] Need to make sure that the client ip extracted from the request is the real IP of the remote client