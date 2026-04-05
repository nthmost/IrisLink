#!/usr/bin/env python3
"""IrisLink UserPromptSubmit hook.

Injected into every Claude Code turn via settings.json hooks.
When an IrisLink session is active (pending.json exists), this adds
context telling Claude to relay the user's message and surface any
new inbound messages before responding normally.

Claude Code passes the hook event as JSON on stdin.
The hook returns JSON on stdout with an optional "additionalContext" key.

No output (exit 0) means: proceed normally, no extra context.
"""

import json
import sys
from pathlib import Path

PENDING_PATH = Path.home() / ".irislink" / "rooms" / "pending.json"
CONFIG_PATH = Path.home() / ".irislink" / "config.json"


def get_connector_url() -> str:
    try:
        cfg = json.loads(CONFIG_PATH.read_text())
        return cfg.get("connector_url", "http://localhost:8357")
    except (FileNotFoundError, json.JSONDecodeError):
        return "http://localhost:8357"


def read_pending() -> dict | None:
    try:
        return json.loads(PENDING_PATH.read_text())
    except (FileNotFoundError, json.JSONDecodeError):
        return None


def main():
    # Read the hook event from stdin
    try:
        event = json.load(sys.stdin)
    except (json.JSONDecodeError, EOFError):
        sys.exit(0)

    prompt = event.get("prompt", "")

    # Let explicit /irislink commands pass through to the skill unchanged
    if prompt.strip().startswith("/irislink"):
        sys.exit(0)

    pending = read_pending()
    if pending is None:
        sys.exit(0)

    otp = pending.get("otp", "")
    if not otp:
        sys.exit(0)

    connector_url = get_connector_url()

    # Read session metadata for handle and mode
    meta_path = Path.home() / ".irislink" / "rooms" / f"{otp}.meta"
    try:
        meta = json.loads(meta_path.read_text())
        handle = meta.get("handle", "operator")
        mode = meta.get("mode", "relay")
        cursor = meta.get("cursor", 0)
    except (FileNotFoundError, json.JSONDecodeError):
        handle = "operator"
        mode = "relay"
        cursor = 0

    # Check for new inbound messages to surface
    log_path = Path.home() / ".irislink" / "rooms" / f"{otp}.log"
    try:
        log_lines = log_path.read_text().strip().splitlines()
        # Show last 5 unread lines as context (simple heuristic)
        recent = log_lines[-5:] if log_lines else []
        inbound_context = "\n".join(recent) if recent else "(none yet)"
    except FileNotFoundError:
        inbound_context = "(none yet)"

    context = f"""## Active IrisLink Session

OTP: {otp}
Your handle: {handle}
Mode: {mode}
Connector: {connector_url}

**The user's message should be relayed to the IrisLink room.**

Steps to take before your normal response:
1. If mode is not `relay`, run:
   `python connectors/irislink_helpers.py mediate {mode} "<user message>"`
   Show the mediated version and ask the user to confirm before sending.
   For `relay` mode, skip this step.
2. Send the message:
   `python connectors/irislink_helpers.py post_message {connector_url} {otp} {handle} "<text to send>"`
3. Check for new inbound messages:
   `curl -s "{connector_url}/events?room_otp={otp}&since={cursor}"`
   Display any new messages from the partner before your response.

Recent inbound messages (from log):
{inbound_context}

After relaying, respond normally to the user's message content if it warrants a response.
To exit relay mode, the user can run `/irislink leave`."""

    print(json.dumps({"additionalContext": context}))


if __name__ == "__main__":
    main()
