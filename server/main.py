import asyncio
import os
import random
import re
import string
import time
from typing import Optional

from fastapi import FastAPI, HTTPException, Path
from fastapi.middleware.cors import CORSMiddleware
from pydantic import BaseModel

app = FastAPI(title="IrisLink Rendezvous Server")

app.add_middleware(
    CORSMiddleware,
    allow_origins=["*"],
    allow_methods=["*"],
    allow_headers=["*"],
)

OTP_ALPHABET = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
OTP_PATTERN = re.compile(r"^[ABCDEFGHJKLMNPQRSTUVWXYZ23456789]{6}$")
TTL_SECONDS = 15 * 60

rooms: dict = {}


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def generate_otp() -> str:
    return "".join(random.choices(OTP_ALPHABET, k=6))


def now_ms() -> int:
    return int(time.time() * 1000)


def now_s() -> float:
    return time.time()


def derive_phase(room: dict) -> str:
    if room["closedAt"] is not None:
        return "closed"
    if now_ms() > room["expiresAt"]:
        return "closed"
    if len(room["participants"]) <= 1:
        return "waiting"
    all_present = all(p["status"] == "present" for p in room["participants"])
    if all_present:
        return "active"
    return "joined"


def public_room(room: dict) -> dict:
    ttl_seconds = max(0, int((room["expiresAt"] - now_ms()) / 1000))
    phase = derive_phase(room)

    waiting_on = None
    if room["messages"]:
        last = room["messages"][-1]
        if last["status"] != "acknowledged":
            p0_handle = room["participants"][0]["handle"] if len(room["participants"]) > 0 else None
            p1_handle = room["participants"][1]["handle"] if len(room["participants"]) > 1 else None
            if last["sender"] == p0_handle:
                waiting_on = p1_handle
            else:
                waiting_on = p0_handle

    return {
        "otp": room["otp"],
        "mode": room["mode"],
        "phase": phase,
        "ttlSeconds": ttl_seconds,
        "participants": room["participants"],
        "messages": room["messages"],
        "waitingOn": waiting_on,
        "createdAt": room["createdAt"],
        "closedAt": room["closedAt"],
    }


def ensure_room(otp: str) -> dict:
    room = rooms.get(otp)
    if not room:
        raise HTTPException(status_code=404, detail="Room not found")
    if room["closedAt"] is not None or now_ms() > room["expiresAt"]:
        if room["closedAt"] is None:
            room["closedAt"] = now_ms()
        raise HTTPException(status_code=410, detail="Room closed")
    return room


def validate_otp(otp: str) -> None:
    if not OTP_PATTERN.match(otp):
        raise HTTPException(status_code=400, detail="Invalid OTP format")


# ---------------------------------------------------------------------------
# TTL sweep background task
# ---------------------------------------------------------------------------

async def ttl_sweep():
    while True:
        await asyncio.sleep(60)
        expired = [
            otp
            for otp, room in rooms.items()
            if room["closedAt"] is None and now_ms() > room["expiresAt"]
        ]
        for otp in expired:
            rooms[otp]["closedAt"] = now_ms()


@app.on_event("startup")
async def startup_event():
    asyncio.create_task(ttl_sweep())


# ---------------------------------------------------------------------------
# Request/response models
# ---------------------------------------------------------------------------

class CreateRoomBody(BaseModel):
    handle: str = "operator"


class JoinRoomBody(BaseModel):
    handle: str


class UpdateParticipantBody(BaseModel):
    handle: str
    status: str


class UpdateModeBody(BaseModel):
    mode: str


class PostMessageBody(BaseModel):
    sender: str
    text: str


# ---------------------------------------------------------------------------
# Routes
# ---------------------------------------------------------------------------

@app.post("/rooms")
def create_room(body: CreateRoomBody):
    otp = generate_otp()
    ts = now_ms()
    room = {
        "otp": otp,
        "mode": "relay",
        "participants": [{"handle": body.handle, "status": "present"}],
        "messages": [],
        "createdAt": ts,
        "expiresAt": ts + TTL_SECONDS * 1000,
        "closedAt": None,
    }
    rooms[otp] = room
    return {"room": public_room(room)}


@app.post("/rooms/{otp}/join")
def join_room(otp: str, body: JoinRoomBody):
    validate_otp(otp)
    room = ensure_room(otp)
    handle = body.handle
    existing = next((p for p in room["participants"] if p["handle"] == handle), None)
    if existing is None:
        if len(room["participants"]) >= 2:
            raise HTTPException(status_code=409, detail="Room already has two handles")
        room["participants"].append({"handle": handle, "status": "joined"})
        room["expiresAt"] = now_ms() + TTL_SECONDS * 1000
    else:
        existing["status"] = "present"
    return {"room": public_room(room)}


@app.post("/rooms/{otp}/participants")
def update_participant(otp: str, body: UpdateParticipantBody):
    validate_otp(otp)
    room = ensure_room(otp)
    participant = next((p for p in room["participants"] if p["handle"] == body.handle), None)
    if participant is None:
        raise HTTPException(status_code=404, detail="Handle not in room")
    participant["status"] = body.status
    return {"room": public_room(room)}


@app.post("/rooms/{otp}/mode")
def update_mode(otp: str, body: UpdateModeBody):
    validate_otp(otp)
    room = ensure_room(otp)
    room["mode"] = body.mode
    return {"room": public_room(room)}


@app.post("/rooms/{otp}/messages")
def post_message(otp: str, body: PostMessageBody):
    validate_otp(otp)
    room = ensure_room(otp)
    ts = now_ms()
    rand_hex = "".join(random.choices(string.hexdigits[:16], k=6))
    msg_id = f"{ts}-{rand_hex}"
    message = {
        "id": msg_id,
        "sender": body.sender,
        "text": body.text,
        "status": "pending",
        "timestamp": ts,
    }
    room["messages"].append(message)
    room["expiresAt"] = now_ms() + TTL_SECONDS * 1000
    return {"message": message}


@app.post("/rooms/{otp}/messages/{message_id}/ack")
def ack_message(otp: str, message_id: str):
    validate_otp(otp)
    room = ensure_room(otp)
    message = next((m for m in room["messages"] if m["id"] == message_id), None)
    if message is None:
        raise HTTPException(status_code=404, detail="Message not found")
    message["status"] = "acknowledged"
    return {"room": public_room(room)}


@app.get("/rooms/{otp}")
def get_room(otp: str):
    validate_otp(otp)
    room = rooms.get(otp)
    if room is None:
        raise HTTPException(status_code=404, detail="Room not found")
    return {"room": public_room(room)}


@app.delete("/rooms/{otp}")
def delete_room(otp: str):
    validate_otp(otp)
    room = rooms.get(otp)
    if room is None:
        # Mirror index.js: 204 when room not found on DELETE
        from fastapi.responses import Response
        return Response(status_code=204)
    if room["closedAt"] is not None or now_ms() > room["expiresAt"]:
        if room["closedAt"] is None:
            room["closedAt"] = now_ms()
        from fastapi.responses import Response
        return Response(status_code=204)
    room["closedAt"] = now_ms()
    return {"room": public_room(room)}
