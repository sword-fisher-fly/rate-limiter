#!/usr/bin/env python3
"""Load generator for the rate limiter mock API.

The script sends GET requests to /api/v1/rate-limiter-mock at a fixed rate and
prints the result of every request. A request rejected by the rate limiter
carries a Retry-After header, the client waits for that many seconds and sends
the same request again until it is served.

The four cases mock the scenarios of the rate limit configs of the mock API,
they are described in the file test/rest.http:
    1) only the path, no client id and no ip
    2) the client id and the ip
    3) the client id, no ip
    4) the ip, no client id

Examples:
    python3 test/python_req.py --rate=10 --host=127.0.0.1:18099 --count=20
    python3 test/python_req.py --case=2 --client_id=client_id_001 --ip=127.0.0.1
"""

import argparse
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor

try:
    import requests
except ImportError:
    sys.exit("the requests library is required, install it with: pip install requests")

# Path of the mock business API that is limited by the rate limiter.
API_PATH = "/api/v1/rate-limiter-mock"

# Header the server takes the client id of a request from.
CLIENT_ID_HEADER = "X-Client-Id"

# Header the server takes the ip of a request from, it falls back to the
# address the request came from.
FORWARDED_FOR_HEADER = "X-Forwarded-For"

# The status code of a request that was served.
HTTP_OK = 200

# The scenarios the rate limit configs of the mock API cover.
CASE_ONLY_PATH = 1
CASE_CLIENT_ID_IP = 2
CASE_CLIENT_ID = 3
CASE_IP = 4

CASE_DESCRIPTIONS = {
    CASE_ONLY_PATH: "only the path, no client id and no ip",
    CASE_CLIENT_ID_IP: "the client id and the ip",
    CASE_CLIENT_ID: "the client id, no ip",
    CASE_IP: "the ip, no client id",
}

# The number of the times a request is retried before it is given up, so a
# request that is rejected forever does not block the client.
MAX_RETRIES = 10

# The number of the requests that are in flight at the same time.
MAX_WORKERS = 5

# The timeout of a single request, unit: s
REQUEST_TIMEOUT = 10

GREEN = "\033[32m"
RED = "\033[31m"
RESET = "\033[0m"

# The log lines are printed by the worker threads, the lock keeps them from
# being mixed up.
PRINT_LOCK = threading.Lock()

# The worker threads are numbered in the order they send their first request,
# so the log lines name the worker instead of the id of the thread, which is
# a large number that changes on every run.
THREAD_ID_LOCK = threading.Lock()
THREAD_IDS = {}
THREAD_ID_SEQ = 0

# The counters of the results of the requests.
RESULT_LOCK = threading.Lock()
RESULT_OK = 0
RESULT_FAIL = 0


def parse_args():
    """Read the arguments of the command line."""
    parser = argparse.ArgumentParser(description="Send requests to the rate limiter mock API.")
    parser.add_argument("--rate", type=int, default=10,
                        help="the number of the requests sent per second (default: 10)")
    parser.add_argument("--host", default="127.0.0.1:18099",
                        help="the address of the rate limiter service (default: 127.0.0.1:18099)")
    parser.add_argument("--count", type=int, default=20,
                        help="the total number of the requests to send (default: 20)")
    parser.add_argument("--client_id", default="client_id_001",
                        help="the client id sent in the %s header (default: client_id_001)" % CLIENT_ID_HEADER)
    parser.add_argument("--ip", default="127.0.0.1",
                        help="the ip sent in the %s header (default: 127.0.0.1)" % FORWARDED_FOR_HEADER)
    parser.add_argument("--case", type=int, choices=sorted(CASE_DESCRIPTIONS), default=CASE_ONLY_PATH,
                        help="the scenario to mock: 1) only the path, 2) the client id and the ip, "
                             "3) the client id, 4) the ip (default: 1)")

    args = parser.parse_args()
    if args.rate <= 0:
        parser.error("--rate must be greater than 0")
    if args.count <= 0:
        parser.error("--count must be greater than 0")

    return args


def build_headers(case, client_id, ip):
    """Build the headers of the request of the given scenario."""
    headers = {}
    if case in (CASE_CLIENT_ID, CASE_CLIENT_ID_IP):
        headers[CLIENT_ID_HEADER] = client_id
    if case in (CASE_IP, CASE_CLIENT_ID_IP):
        headers[FORWARDED_FOR_HEADER] = ip

    return headers


def parse_retry_after(response):
    """Return the number of the seconds the client has to wait before it sends
    the request again, None when the response does not ask the client to wait."""
    value = response.headers.get("Retry-After")
    if value is None:
        return None

    try:
        seconds = int(value.strip())
    except ValueError:
        return None

    return max(seconds, 0)


def thread_id():
    """Return the number of the worker thread that calls it. The number is
    given to a thread when it sends its first request, so the same thread
    keeps it for the whole run."""
    global THREAD_ID_SEQ

    ident = threading.get_ident()
    with THREAD_ID_LOCK:
        number = THREAD_IDS.get(ident)
        if number is None:
            THREAD_ID_SEQ += 1
            number = THREAD_ID_SEQ
            THREAD_IDS[ident] = number

    return number


def log_ok(index):
    """Print the result of a request that was served."""
    global RESULT_OK

    line = "%s[Thread %d] [%d] [OK]%s" % (GREEN, thread_id(), index, RESET)

    with PRINT_LOCK:
        print(line, flush=True)

    with RESULT_LOCK:
        RESULT_OK += 1


def log_fail(index, retry_after=None):
    """Print the result of a request that was rejected."""
    global RESULT_FAIL

    worker = thread_id()
    if retry_after is None:
        line = "[Thread %d] [%d] [FAIL]" % (worker, index)
    else:
        line = "[Thread %d] [%d] [FAIL] [Try After %ds]" % (worker, index, retry_after)

    with PRINT_LOCK:
        print("%s%s%s" % (RED, line, RESET), flush=True)

    with RESULT_LOCK:
        RESULT_FAIL += 1


def send_request(index, session, url, headers):
    """Send a request and send it again while the server asks the client to
    wait for the rate limiter to let it through."""
    for _ in range(MAX_RETRIES + 1):
        try:
            response = session.get(url, headers=headers, timeout=REQUEST_TIMEOUT)
        except requests.RequestException as err:
            log_fail(index)
            print("the request %d failed: %s" % (index, err), file=sys.stderr)
            return

        retry_after = parse_retry_after(response)
        if retry_after is None:
            if response.status_code == HTTP_OK:
                log_ok(index)
            else:
                log_fail(index)
                print("the request %d failed with the status code %d" % (index, response.status_code),
                      file=sys.stderr)
            return

        # The rate limiter rejected the request, it tells the client how long
        # it has to wait for the bucket to hold a token again.
        log_fail(index, retry_after)
        time.sleep(retry_after + 1)


def build_url(host):
    """Build the url of the mock API out of the address of the service."""
    if "://" not in host:
        host = "http://" + host

    return host.rstrip("/") + API_PATH


def main():
    args = parse_args()
    url = build_url(args.host)
    headers = build_headers(args.case, args.client_id, args.ip)

    print("target:  %s" % url)
    print("case:    %d (%s)" % (args.case, CASE_DESCRIPTIONS[args.case]))
    print("rate:    %d requests per second, %d requests in total" % (args.rate, args.count))
    if headers:
        print("headers: %s" % ", ".join("%s: %s" % (key, value) for key, value in headers.items()))
    print()

    session = requests.Session()
    interval = 1.0 / args.rate
    start = time.monotonic()

    with ThreadPoolExecutor(max_workers=min(args.count, MAX_WORKERS)) as executor:
        for index in range(1, args.count + 1):
            # The requests are sent at a fixed rate, the index of the request
            # is the number of the attempt of the client.
            target = start + (index - 1) * interval
            delay = target - time.monotonic()
            if delay > 0:
                time.sleep(delay)
            executor.submit(send_request, index, session, url, headers)

    print("done: %d ok, %d failed" % (RESULT_OK, RESULT_FAIL))

    return 0


if __name__ == "__main__":
    sys.exit(main())
