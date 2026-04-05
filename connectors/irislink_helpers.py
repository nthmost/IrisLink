#!/usr/bin/env python3
"""IrisLink Claude Code skill helper utilities.

Usage: python irislink_helpers.py <command> [args]
"""

import json
import os
import secrets
import sys


CROCKFORD_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
IRISLINK_DIR = os.path.expanduser("~/.irislink/rooms")
LITELLM_BASE_URL = "http://spartacus.local:4000/v1"
_CONFIG_PATH = os.path.expanduser("~/.irislink/config.json")


def get_connector_url() -> str:
    try:
        with open(_CONFIG_PATH) as f:
            return json.load(f).get("connector_url", "http://localhost:8357")
    except (FileNotFoundError, json.JSONDecodeError):
        return "http://localhost:8357"


# ---------------------------------------------------------------------------
# 1. generate_otp
# ---------------------------------------------------------------------------

def generate_otp() -> str:
    """Generate a random 6-char Crockford Base32 OTP."""
    return "".join(secrets.choice(CROCKFORD_ALPHABET) for _ in range(6))


# ---------------------------------------------------------------------------
# 2. derive_room_id
# ---------------------------------------------------------------------------

def derive_room_id(otp: str) -> str:
    """HKDF-SHA256 derivation of a 16-byte room ID from an OTP."""
    from cryptography.hazmat.primitives.hashes import SHA256
    from cryptography.hazmat.primitives.kdf.hkdf import HKDF

    hkdf = HKDF(
        algorithm=SHA256(),
        length=16,
        salt=b"irislink:v0",
        info=b"irislink-room",
    )
    derived = hkdf.derive(otp.upper().encode())
    return derived.hex()


# ---------------------------------------------------------------------------
# 3. write_pending
# ---------------------------------------------------------------------------

def write_pending(otp: str, room_id: str) -> None:
    """Write ~/.irislink/rooms/pending.json with otp and room_id."""
    os.makedirs(IRISLINK_DIR, exist_ok=True)
    pending_path = os.path.join(IRISLINK_DIR, "pending.json")
    with open(pending_path, "w") as f:
        json.dump({"otp": otp, "room_id": room_id}, f)


# ---------------------------------------------------------------------------
# 4. clear_pending
# ---------------------------------------------------------------------------

def clear_pending() -> None:
    """Remove ~/.irislink/rooms/pending.json if it exists."""
    pending_path = os.path.join(IRISLINK_DIR, "pending.json")
    try:
        os.remove(pending_path)
    except FileNotFoundError:
        pass


# ---------------------------------------------------------------------------
# 5. post_message
# ---------------------------------------------------------------------------

def post_message(connector_url: str, room_otp: str, sender: str, text: str) -> None:
    """POST a message to the connector's /message endpoint."""
    import httpx

    url = f"{connector_url.rstrip('/')}/message"
    payload = {"room_otp": room_otp, "sender": sender, "text": text}
    response = httpx.post(url, json=payload)
    response.raise_for_status()
    print(response.text)


# ---------------------------------------------------------------------------
# 6. poll_events
# ---------------------------------------------------------------------------

def poll_events(connector_url: str, room_otp: str, since: str = "") -> None:
    """GET events from the connector's /events endpoint."""
    import httpx

    params: dict = {"room_otp": room_otp}
    if since:
        params["since"] = since

    url = f"{connector_url.rstrip('/')}/events"
    response = httpx.get(url, params=params)
    response.raise_for_status()
    print(response.text)


# ---------------------------------------------------------------------------
# 7. mediate
# ---------------------------------------------------------------------------

def mediate(mode: str, text: str) -> None:
    """Transform a message via LiteLLM router according to the given mode."""
    if mode == "relay":
        print(text)
        return

    from openai import OpenAI

    client = OpenAI(base_url=LITELLM_BASE_URL, api_key="dummy")

    if mode == "mediate":
        model = "loki/qwen-coder-14b"
        system_prompt = (
            "You are a thoughtful relay. Rewrite the following message to be clearer "
            "and more considerate, keeping the original meaning. Output only the rewritten message."
        )
    elif mode == "game-master":
        model = "loki/qwen3-coder-30b"
        system_prompt = (
            "You are a creative game master mediating a collaborative session. "
            "Add a brief narrative flourish or creative prompt to accompany this message. "
            "Output the original message followed by a GM note in italics."
        )
    else:
        print(f"Unknown mediate mode: {mode}", file=sys.stderr)
        sys.exit(1)

    response = client.chat.completions.create(
        model=model,
        messages=[
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": text},
        ],
    )
    print(response.choices[0].message.content)


# ---------------------------------------------------------------------------
# CLI dispatch
# ---------------------------------------------------------------------------

def main() -> None:
    args = sys.argv[1:]

    if not args:
        print("Usage: python irislink_helpers.py <command> [args]", file=sys.stderr)
        sys.exit(1)

    command = args[0]

    try:
        if command == "generate_otp":
            print(generate_otp())

        elif command == "derive_room_id":
            if len(args) < 2:
                print("Usage: derive_room_id <otp>", file=sys.stderr)
                sys.exit(1)
            print(derive_room_id(args[1]))

        elif command == "write_pending":
            if len(args) < 3:
                print("Usage: write_pending <otp> <room_id>", file=sys.stderr)
                sys.exit(1)
            write_pending(args[1], args[2])
            print("ok")

        elif command == "clear_pending":
            clear_pending()
            print("ok")

        elif command == "post_message":
            if len(args) < 5:
                print("Usage: post_message <connector_url> <room_otp> <sender> <text>", file=sys.stderr)
                sys.exit(1)
            post_message(args[1], args[2], args[3], args[4])

        elif command == "poll_events":
            if len(args) < 3:
                print("Usage: poll_events <connector_url> <room_otp> [since]", file=sys.stderr)
                sys.exit(1)
            since = args[3] if len(args) >= 4 else ""
            poll_events(args[1], args[2], since)

        elif command == "mediate":
            if len(args) < 3:
                print("Usage: mediate <mode> <text>", file=sys.stderr)
                sys.exit(1)
            mediate(args[1], args[2])

        else:
            print(f"Unknown command: {command}", file=sys.stderr)
            sys.exit(1)

    except Exception as e:
        print(f"Error: {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
