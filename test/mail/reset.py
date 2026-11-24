# SPDX-License-Identifier: AGPL-3.0-only
"""Lab check of e-mail end to end: asks for a password reset for the lab's
panel user, reads the message the lab mail catcher received, follows its
link and signs in. Needs the lab, `make api-run` and `make seed-user`.

Reads PANEL_USER_EMAIL and PANEL_USER_PASSWORD from the environment
(test/seed/dev.env). The password is set to the same lab value again.
"""
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

API, BOX = "http://127.0.0.1:58080", "http://127.0.0.1:58025"


def call(url, body=None, method=None):
    req = urllib.request.Request(url, method=method, headers={"Content-Type": "application/json"},
                                 data=None if body is None else json.dumps(body).encode())
    try:
        with urllib.request.urlopen(req) as resp:
            raw = resp.read()
            try:
                return resp.status, json.loads(raw)
            except ValueError:
                return resp.status, None
    except urllib.error.HTTPError as e:
        return e.code, None


failed = False


def check(what, got, want):
    global failed
    ok = got == want
    failed |= not ok
    print(("PASS " if ok else "FAIL ") + f" {what}" + ("" if ok else f": got {got!r}, want {want!r}"))


email, password = os.environ["PANEL_USER_EMAIL"], os.environ["PANEL_USER_PASSWORD"]
call(BOX + "/api/v1/messages", method="DELETE")
# An earlier link may still be fresh; the API then sends no second one.
check("reset asked for", call(API + "/v1/password/forgot", {"email": email})[0], 204)
inbox = {"total": 0}
for _ in range(15):
    time.sleep(1)
    inbox = call(BOX + "/api/v1/messages")[1]
    if inbox["total"]:
        break
check("one message caught", inbox["total"], 1)
if inbox["total"]:
    message = inbox["messages"][0]
    check("recipient", message["To"][0]["Address"], email)
    check("subject", message["Subject"], "Parola sıfırlama")
    text = call(BOX + "/api/v1/message/" + message["ID"])[1]["Text"]
    link = re.search(r"/reset\?token=([0-9a-f]{64})", text)
    check("link in the message", bool(link), True)
    if link:
        check("new password set", call(API + "/v1/password/reset", {"token": link.group(1), "password": password})[0], 204)
        check("link works once", call(API + "/v1/password/reset", {"token": link.group(1), "password": password})[0], 410)
        check("sign-in with the new password", call(API + "/v1/login", {"email": email, "password": password})[0], 200)
print("RESULT:", "FAIL" if failed else "PASS")
sys.exit(failed)
