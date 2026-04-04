import express from 'express'
import cors from 'cors'
import { customAlphabet } from 'nanoid'

const app = express()
const PORT = process.env.PORT || 4173
const OTP_ALPHABET = 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789'
const generateOtp = customAlphabet(OTP_ALPHABET, 6)

app.use(cors())
app.use(express.json())

const rooms = new Map()
const TTL_MS = 15 * 60 * 1000

const derivePhase = (room) => {
  if (room.closedAt) return 'closed'
  if (Date.now() > room.expiresAt) return 'closed'
  if (room.participants.length <= 1) return 'waiting'
  const allPresent = room.participants.every((p) => p.status === 'present')
  if (allPresent) return 'active'
  return 'joined'
}

const publicRoom = (room) => {
  if (!room) return null
  const ttlSeconds = Math.max(0, Math.floor((room.expiresAt - Date.now()) / 1000))
  const phase = derivePhase(room)
  const waitingOn = (() => {
    if (!room.messages.length) return null
    const last = room.messages[room.messages.length - 1]
    if (last.status !== 'acknowledged') {
      return last.sender === room.participants[0]?.handle
        ? room.participants[1]?.handle || null
        : room.participants[0]?.handle || null
    }
    return null
  })()
  return {
    otp: room.otp,
    mode: room.mode,
    phase,
    ttlSeconds,
    participants: room.participants,
    messages: room.messages,
    waitingOn,
    createdAt: room.createdAt,
    closedAt: room.closedAt || null
  }
}

const ensureRoom = (otp) => {
  const room = rooms.get(otp)
  if (!room) {
    const error = new Error('Room not found')
    error.status = 404
    throw error
  }
  if (room.closedAt || Date.now() > room.expiresAt) {
    room.closedAt = room.closedAt || Date.now()
    const error = new Error('Room closed')
    error.status = 410
    throw error
  }
  return room
}

app.post('/rooms', (req, res, next) => {
  try {
    const handle = req.body?.handle || 'operator'
    const otp = generateOtp()
    const now = Date.now()
    const room = {
      otp,
      mode: 'relay',
      participants: [{ handle, status: 'present' }],
      messages: [],
      createdAt: now,
      expiresAt: now + TTL_MS,
      closedAt: null
    }
    rooms.set(otp, room)
    res.json({ room: publicRoom(room) })
  } catch (error) {
    next(error)
  }
})

app.post('/rooms/:otp/join', (req, res, next) => {
  try {
    const room = ensureRoom(req.params.otp)
    const handle = req.body?.handle
    if (!handle) {
      return res.status(400).json({ error: 'Handle required' })
    }
    const existing = room.participants.find((p) => p.handle === handle)
    if (!existing) {
      if (room.participants.length >= 2) {
        return res.status(409).json({ error: 'Room already has two handles' })
      }
      room.participants.push({ handle, status: 'joined' })
      room.expiresAt = Date.now() + TTL_MS
    } else {
      existing.status = 'present'
    }
    res.json({ room: publicRoom(room) })
  } catch (error) {
    next(error)
  }
})

app.post('/rooms/:otp/participants', (req, res, next) => {
  try {
    const room = ensureRoom(req.params.otp)
    const { handle, status } = req.body || {}
    if (!handle || !status) {
      return res.status(400).json({ error: 'Handle and status required' })
    }
    const participant = room.participants.find((p) => p.handle === handle)
    if (!participant) {
      return res.status(404).json({ error: 'Handle not in room' })
    }
    participant.status = status
    res.json({ room: publicRoom(room) })
  } catch (error) {
    next(error)
  }
})

app.post('/rooms/:otp/mode', (req, res, next) => {
  try {
    const room = ensureRoom(req.params.otp)
    const mode = req.body?.mode
    if (!mode) return res.status(400).json({ error: 'Mode required' })
    room.mode = mode
    res.json({ room: publicRoom(room) })
  } catch (error) {
    next(error)
  }
})

app.post('/rooms/:otp/messages', (req, res, next) => {
  try {
    const room = ensureRoom(req.params.otp)
    const { sender, text } = req.body || {}
    if (!sender || !text) return res.status(400).json({ error: 'sender and text required' })
    const id = `${Date.now()}-${Math.random().toString(16).slice(2, 8)}`
    const message = {
      id,
      sender,
      text,
      status: 'pending',
      timestamp: Date.now()
    }
    room.messages.push(message)
    room.expiresAt = Date.now() + TTL_MS
    res.json({ message })
  } catch (error) {
    next(error)
  }
})

app.post('/rooms/:otp/messages/:messageId/ack', (req, res, next) => {
  try {
    const room = ensureRoom(req.params.otp)
    const message = room.messages.find((m) => m.id === req.params.messageId)
    if (!message) return res.status(404).json({ error: 'Message not found' })
    message.status = 'acknowledged'
    res.json({ room: publicRoom(room) })
  } catch (error) {
    next(error)
  }
})

app.delete('/rooms/:otp', (req, res, next) => {
  try {
    const room = ensureRoom(req.params.otp)
    room.closedAt = Date.now()
    res.json({ room: publicRoom(room) })
  } catch (error) {
    if (error.status === 404) {
      return res.status(204).end()
    }
    next(error)
  }
})

app.get('/rooms/:otp', (req, res, next) => {
  try {
    const room = rooms.get(req.params.otp)
    if (!room) return res.status(404).json({ error: 'Room not found' })
    res.json({ room: publicRoom(room) })
  } catch (error) {
    next(error)
  }
})

app.use((error, _req, res, _next) => {
  const status = error.status || 500
  res.status(status).json({ error: error.message || 'Unknown error' })
})

app.listen(PORT, () => {
  console.log(`IrisLink rendezvous server listening on :${PORT}`)
})
