#!/usr/bin/env python3
"""IrisLink local connector proxy.

Bridges the Claude Code /irislink skill to the IrisLink rendezvous API.
Listens on localhost:8357 by default.

Usage:
    python claude_proxy.py
    python claude_proxy.py --listen 8357
    IRISLINK_BASE_URL=http://localhost:4173 python claude_proxy.py
"""

import argparse
import json
import os
from pathlib import Path

import httpx
import uvicorn
from fastapi import FastAPI, HTTPException, Query
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel

VERSION = "0.1.0"
PENDING_PATH = Path.home() / ".irislink" / "rooms" / "pending.json"


def get_base_url() -> str:
    return os.environ.get("IRISLINK_BASE_URL", "http://localhost:4173")


def read_pending() -> dict | None:
    try:
        return json.loads(PENDING_PATH.read_text())
    except (FileNotFoundError, json.JSONDecodeError):
        return None


app = FastAPI(title="IrisLink Connector Proxy", version=VERSION)

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)


# ---------------------------------------------------------------------------
# GET /status
# ---------------------------------------------------------------------------

@app.get("/status")
def status():
    pending = read_pending()
    return {
        "version": VERSION,
        "room_attached": pending is not None,
        "room_otp": pending.get("otp") if pending else None,
        "room_id": pending.get("room_id") if pending else None,
        "rendezvous_url": get_base_url(),
    }


# ---------------------------------------------------------------------------
# GET /rooms/pending.json  — lobby reads this to show the OTP
# ---------------------------------------------------------------------------

@app.get("/rooms/pending.json")
def get_pending():
    pending = read_pending()
    if pending is None:
        raise HTTPException(status_code=404, detail="No pending room")
    return pending


# ---------------------------------------------------------------------------
# POST /message  — skill pushes outbound messages here
# ---------------------------------------------------------------------------

class MessageRequest(BaseModel):
    room_otp: str
    sender: str
    text: str


@app.post("/message")
async def post_message(req: MessageRequest):
    url = f"{get_base_url()}/rooms/{req.room_otp}/messages"
    async with httpx.AsyncClient() as client:
        resp = await client.post(url, json={"sender": req.sender, "text": req.text})
    if resp.status_code >= 400:
        raise HTTPException(status_code=resp.status_code, detail=resp.text)
    return resp.json()


# ---------------------------------------------------------------------------
# GET /events  — skill polls here for inbound messages
#
# ?room_otp=ABC123&since=<unix_ms_timestamp>
#
# Returns messages with timestamp > since, plus the next cursor and
# current room phase so the skill can detect state changes.
# ---------------------------------------------------------------------------

@app.get("/events")
async def get_events(
    room_otp: str = Query(..., description="6-char OTP"),
    since: int = Query(0, description="Unix ms timestamp — return messages after this"),
):
    url = f"{get_base_url()}/rooms/{room_otp}"
    async with httpx.AsyncClient() as client:
        resp = await client.get(url)

    if resp.status_code == 404:
        raise HTTPException(status_code=404, detail="Room not found")
    if resp.status_code == 410:
        raise HTTPException(status_code=410, detail="Room closed")
    if resp.status_code >= 400:
        raise HTTPException(status_code=resp.status_code, detail=resp.text)

    room = resp.json().get("room", {})
    messages = room.get("messages", [])

    new_messages = [m for m in messages if m["timestamp"] > since]
    next_cursor = max((m["timestamp"] for m in new_messages), default=since)

    return {
        "events": new_messages,
        "next": next_cursor,
        "phase": room.get("phase"),
        "ttlSeconds": room.get("ttlSeconds"),
        "waitingOn": room.get("waitingOn"),
    }


# ---------------------------------------------------------------------------
# POST /ack  — skill acks a message on behalf of the local participant
# ---------------------------------------------------------------------------

class AckRequest(BaseModel):
    room_otp: str
    message_id: str


@app.post("/ack")
async def ack_message(req: AckRequest):
    url = f"{get_base_url()}/rooms/{req.room_otp}/messages/{req.message_id}/ack"
    async with httpx.AsyncClient() as client:
        resp = await client.post(url)
    if resp.status_code >= 400:
        raise HTTPException(status_code=resp.status_code, detail=resp.text)
    return resp.json()


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(description="IrisLink connector proxy")
    parser.add_argument(
        "--listen",
        type=int,
        default=int(os.environ.get("CONNECTOR_PORT", 8357)),
        help="Port to listen on (default: 8357)",
    )
    parser.add_argument(
        "--host",
        default="127.0.0.1",
        help="Host to bind (default: 127.0.0.1)",
    )
    args = parser.parse_args()

    print(f"IrisLink connector v{VERSION} listening on {args.host}:{args.listen}")
    print(f"Rendezvous URL: {get_base_url()}")
    uvicorn.run(app, host=args.host, port=args.listen, log_level="warning")


if __name__ == "__main__":
    main()
