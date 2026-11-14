# -*- coding: utf-8 -*-
"""
uds-skill-auth — Python helpers for UDS-backed skills.

Resolve SSO credentials via loopback Host APIs, or call intranet APIs through
the outbound proxy.

Environment:
  DSH_SESSION_ID         — agent session id (injected by DSH shell-env)
  DSH_TEAM_TASK_CAPABILITY    — short-lived Host-minted capability (preferred for cron/skill)
  DSH_WEB_URL            — optional base URL of the Harness web server
  DSH_TEAM_BASE          — optional override, e.g. http://127.0.0.1:PORT/dsh-team
"""
from __future__ import annotations

import json
import os
import ssl
import urllib.error
import urllib.request
from typing import Any, Dict, Optional, Tuple
from urllib.parse import urlparse


class DshTeamError(RuntimeError):
    def __init__(self, message: str, *, code: str = "dsh_team_error", status: int = 0):
        super().__init__(message)
        self.code = code
        self.status = status


def _base_url() -> str:
    """Prefer loopback Host. Env override cannot point at non-loopback without explicit allow."""
    managed = os.environ.get("DSH_TEAM_BASE")
    explicit = (managed or "").strip().rstrip("/")
    web = (os.environ.get("DSH_WEB_URL") or "").strip().rstrip("/")
    base = explicit or (web + "/dsh-team" if web else "")
    if managed is not None and not explicit:
        raise DshTeamError("Host did not authorize this session", code="no_session")
    if not base:
        raise DshTeamError("Missing Host endpoint; load dsh-team shellEnv adapter", code="no_base")
    parsed = urlparse(base)
    if parsed.scheme not in ("http", "https") or parsed.hostname not in ("127.0.0.1", "localhost", "::1") or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise DshTeamError("Team endpoint must be an owned loopback HTTP(S) URL", code="unsafe_base")
    return base


def _session_id() -> str:
    return (os.environ.get("DSH_SESSION_ID") or "").strip()


def _task_capability() -> str:
    managed = os.environ.get("DSH_TEAM_TASK_CAPABILITY")
    return (managed or "").strip()


def _auth_headers(extra: Optional[Dict[str, str]] = None) -> Dict[str, str]:
    hdrs = dict(extra or {})
    sid = _session_id()
    if sid:
        hdrs.setdefault("X-DSH-Session-Id", sid)
    cap = _task_capability()
    if cap:
        hdrs["X-Dsh-Team-Task-Capability"] = cap
    hdrs.setdefault("Accept", "application/json")
    return hdrs


def _is_loopback_url(url: str) -> bool:
    try:
        host = (urlparse(url).hostname or "").lower()
    except Exception:
        return False
    return host in ("127.0.0.1", "localhost", "::1")


def _opener_no_proxy():
    return urllib.request.build_opener(urllib.request.ProxyHandler({}))


def _http_json(
    method: str,
    url: str,
    *,
    headers: Optional[Dict[str, str]] = None,
    body: Any = None,
    timeout: float = 30.0,
) -> Tuple[int, Any, str]:
    data = None
    hdrs = dict(headers or {})
    if body is not None:
        if isinstance(body, (dict, list)):
            data = json.dumps(body, ensure_ascii=False).encode("utf-8")
            hdrs.setdefault("Content-Type", "application/json;charset=UTF-8")
        elif isinstance(body, str):
            data = body.encode("utf-8")
        elif isinstance(body, bytes):
            data = body
        else:
            data = json.dumps(body, ensure_ascii=False).encode("utf-8")
            hdrs.setdefault("Content-Type", "application/json;charset=UTF-8")
    req = urllib.request.Request(url, data=data, headers=hdrs, method=method.upper())
    # SEC-18: always use standard TLS verification — never unverified context.
    ctx = ssl.create_default_context()
    try:
        if _is_loopback_url(url):
            opener = _opener_no_proxy()
            with opener.open(req, timeout=timeout) as resp:
                raw = resp.read().decode("utf-8", errors="replace")
                status = getattr(resp, "status", 200) or 200
        else:
            with urllib.request.urlopen(req, timeout=timeout, context=ctx) as resp:
                raw = resp.read().decode("utf-8", errors="replace")
                status = getattr(resp, "status", 200) or 200
    except urllib.error.HTTPError as e:
        raw = (e.fp.read().decode("utf-8", errors="replace") if e.fp else "")
        status = e.code
    except urllib.error.URLError as e:
        raise DshTeamError(f"无法连接 dsh-team: {e.reason}", code="uds_unreachable") from e

    parsed: Any = None
    if raw.strip():
        try:
            parsed = json.loads(raw)
        except json.JSONDecodeError:
            parsed = None
    return status, parsed, raw


def resolve(*, apply_env_aliases: bool = False) -> Dict[str, str]:
    """
    Fetch {empNo, token} for the current DSH session from Host.
    Prefer DSH_TEAM_TASK_CAPABILITY (Host-minted) over a browser session cookie.
    apply_env_aliases defaults False (SEC-18) — subprocesses inherit secrets if True.
    Prefer request()/outbound instead of exposing raw tokens when possible.
    """
    sid = _session_id()
    cap = _task_capability()
    if not sid and not cap:
        raise DshTeamError(
            "缺少 DSH_SESSION_ID 或 DSH_TEAM_TASK_CAPABILITY",
            code="no_session",
        )

    url = _base_url() + "/agent-credentials"
    body: Dict[str, Any] = {}
    if sid:
        body["sessionId"] = sid
    if cap:
        body["taskCapability"] = cap
    status, parsed, raw = _http_json(
        "POST",
        url,
        headers=_auth_headers(),
        body=body,
        timeout=15.0,
    )
    if status == 401 or (isinstance(parsed, dict) and parsed.get("error") == "no_skill_credentials"):
        msg = (parsed or {}).get("message") if isinstance(parsed, dict) else None
        raise DshTeamError(msg or "请先完成 UDS 扫码登录", code="no_credentials", status=status)
    if status == 403 and isinstance(parsed, dict) and parsed.get("error") == "raw_token_disabled":
        raise DshTeamError(
            "原始凭证接口已关闭，请使用 outbound()",
            code="raw_token_disabled",
            status=403,
        )
    if status != 200 or not isinstance(parsed, dict):
        raise DshTeamError(
            f"获取凭证失败 (HTTP {status})",
            code="credentials_http",
            status=status,
        )
    emp_no = str(parsed.get("empNo") or "").strip()
    token = str(parsed.get("token") or "").strip()
    if not emp_no or not token:
        raise DshTeamError("凭证响应不完整", code="bad_credentials")

    if apply_env_aliases:
        os.environ["EMP_NO"] = emp_no
        os.environ["AUTH_VALUE"] = token
        os.environ["coclaw_empno"] = emp_no
        os.environ["coclaw_token"] = token

    return {"empNo": emp_no, "token": token, "updatedAt": str(parsed.get("updatedAt") or "")}


def request(
    method: str,
    url: str,
    *,
    headers: Optional[Dict[str, str]] = None,
    body: Any = None,
    timeout: float = 30.0,
) -> Dict[str, Any]:
    """
    Call an intranet URL via Host outbound proxy (injects X-Emp-No / X-Auth-Value).
    Returns {statusCode, headers, body, json}.
    """
    sid = _session_id()
    cap = _task_capability()
    if not sid and not cap:
        raise DshTeamError(
            "缺少 DSH_SESSION_ID 或 DSH_TEAM_TASK_CAPABILITY",
            code="no_session",
        )

    host = urlparse(url).hostname or ""
    payload: Dict[str, Any] = {
        "method": method.upper(),
        "url": url,
        "headers": headers or {},
        "body": body,
        "timeoutMs": int(timeout * 1000),
    }
    if sid:
        payload["sessionId"] = sid
    if cap:
        payload["taskCapability"] = cap
    status, parsed, raw = _http_json(
        "POST",
        _base_url() + "/outbound",
        headers=_auth_headers(),
        body=payload,
        timeout=timeout + 5.0,
    )
    if status == 401 or (isinstance(parsed, dict) and parsed.get("error") == "no_skill_credentials"):
        msg = (parsed or {}).get("message") if isinstance(parsed, dict) else None
        raise DshTeamError(msg or "请先完成 UDS 扫码登录", code="no_credentials", status=status)
    if status == 403 and isinstance(parsed, dict) and parsed.get("error") == "host_not_allowed":
        raise DshTeamError(f"主机不在 outbound 白名单: {host}", code="host_not_allowed", status=403)
    if not isinstance(parsed, dict) or "statusCode" not in parsed:
        raise DshTeamError(
            f"outbound 失败 (HTTP {status}): {raw[:200]}",
            code="outbound_http",
            status=status,
        )
    return parsed
