import { NavLink, Route, Routes } from 'react-router-dom'
import { useCallback, useEffect, useMemo, useState } from 'react'
import './App.css'

const MODES = ['relay', 'mediate', 'game-master'] as const
type Mode = (typeof MODES)[number]

type Room = {
  otp: string
  mode: Mode
  phase: string
  ttlSeconds: number
  participants: { handle: string; status: string }[]
  messages: { id: string; sender: string; text: string; status: string; timestamp: number }[]
  waitingOn: string | null
}

const API_BASE = import.meta.env.VITE_API_BASE || '/api'

const formatTtl = (seconds: number | undefined) => {
  if (seconds === undefined || Number.isNaN(seconds)) return '—'
  const safe = Math.max(0, seconds)
  const mins = String(Math.floor(safe / 60)).padStart(2, '0')
  const secs = String(safe % 60).padStart(2, '0')
  return `${mins}:${secs}`
}

function App() {
  return (
    <div className="page">
      <header className="masthead">
        <div className="wordmark">
          <span>IrisLink</span>
          <small>Claude-to-Claude OTP relay</small>
        </div>
        <nav>
          <NavLink to="/" end>
            Console
          </NavLink>
          <NavLink to="/protocol">Protocol</NavLink>
          <NavLink to="/safety">Safety</NavLink>
          <a className="primary" href="https://github.com/nthmost/IrisLink" target="_blank" rel="noreferrer">
            Repo
          </a>
        </nav>
      </header>

      <Routes>
        <Route path="/" element={<ConsolePage />} />
        <Route path="/protocol" element={<ProtocolPage />} />
        <Route path="/safety" element={<SafetyPage />} />
      </Routes>

      <Footer />
    </div>
  )
}

function ConsolePage() {
  const [handle, setHandle] = useState('north-star')
  const [room, setRoom] = useState<Room | null>(null)
  const [otpInput, setOtpInput] = useState('')
  const [messageText, setMessageText] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [statusText, setStatusText] = useState<string | null>(null)

  const myHandle = handle.trim() || 'operator'

  const fetchRoom = useCallback(async (otp: string) => {
    try {
      const response = await fetch(`${API_BASE}/rooms/${otp}`)
      if (!response.ok) throw new Error('room fetch failed')
      const data = (await response.json()) as { room: Room }
      setRoom(data.room)
    } catch (fetchError) {
      console.error(fetchError)
      setStatusText('room unavailable')
    }
  }, [])

  useEffect(() => {
    if (!room?.otp) return
    const id = window.setInterval(() => {
      fetchRoom(room.otp)
    }, 2500)
    return () => window.clearInterval(id)
  }, [room?.otp, fetchRoom])

  const mintRoom = async () => {
    try {
      setError(null)
      const response = await fetch(`${API_BASE}/rooms`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ handle: myHandle })
      })
      if (!response.ok) throw new Error('failed to mint room')
      const data = (await response.json()) as { room: Room }
      setRoom(data.room)
      setOtpInput(data.room.otp)
      setStatusText('room minted')
    } catch (err) {
      setError('Unable to mint OTP')
    }
  }

  const joinRoom = async () => {
    const code = otpInput.trim().toUpperCase()
    if (code.length !== 6) {
      setError('OTP must be 6 characters')
      return
    }
    try {
      setError(null)
      const response = await fetch(`${API_BASE}/rooms/${code}/join`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ handle: myHandle })
      })
      if (!response.ok) throw new Error('join failed')
      const data = (await response.json()) as { room: Room }
      setRoom(data.room)
      setStatusText('joined room')
    } catch (err) {
      setError('Unable to join room')
    }
  }

  const closeRoom = async () => {
    if (!room?.otp) return
    await fetch(`${API_BASE}/rooms/${room.otp}`, { method: 'DELETE' })
    fetchRoom(room.otp)
    setStatusText('room closed')
  }

  const handleModeChange = async (mode: Mode) => {
    if (!room?.otp) return
    await fetch(`${API_BASE}/rooms/${room.otp}/mode`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ mode })
    })
    fetchRoom(room.otp)
  }

  const copyOtp = () => {
    if (!room?.otp) return
    navigator?.clipboard?.writeText(room.otp).catch(() => {})
  }

  const sendMessage = async () => {
    if (!room?.otp || !messageText.trim()) return
    await fetch(`${API_BASE}/rooms/${room.otp}/messages`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sender: myHandle, text: messageText.trim() })
    })
    setMessageText('')
    fetchRoom(room.otp)
  }

  const ackLatest = async () => {
    if (!room?.otp) return
    const pending = [...(room.messages || [])]
      .reverse()
      .find((message) => message.sender !== myHandle && message.status !== 'acknowledged')
    if (!pending) return
    await fetch(`${API_BASE}/rooms/${room.otp}/messages/${pending.id}/ack`, {
      method: 'POST'
    })
    fetchRoom(room.otp)
  }

  const messages = useMemo(() => room?.messages ?? [], [room?.messages])
  const partnerChips = room?.participants ?? []
  const waitingIndicator = room?.waitingOn
    ? room.waitingOn === myHandle
      ? 'You owe the next move.'
      : `${room.waitingOn} owes the next move.`
    : 'No outstanding deliveries.'

  return (
    <main>
      <section className="console">
        <div className="controls-panel card">
          <p className="eyebrow">connection</p>
          <label className="field">
            <span>your handle</span>
            <input value={handle} onChange={(event) => setHandle(event.target.value)} />
          </label>
          <div className="otp-controls">
            <label className="field inline">
              <span>otp</span>
              <input value={otpInput} onChange={(event) => setOtpInput(event.target.value.toUpperCase())} placeholder="ABC123" />
            </label>
            <button className="solid" onClick={joinRoom}>
              join
            </button>
            <button className="solid" onClick={mintRoom}>
              mint
            </button>
            <button className="solid" onClick={closeRoom} disabled={!room || room.phase === 'closed'}>
              close
            </button>
          </div>
          <div className="mode-block">
            <span className="label">mode</span>
            <div className="mode-buttons">
              {MODES.map((option) => (
                <button
                  key={option}
                  className={option === (room?.mode ?? 'relay') ? 'solid selected' : 'solid'}
                  onClick={() => handleModeChange(option)}
                  disabled={!room}
                >
                  {option}
                </button>
              ))}
            </div>
          </div>
          <div className="message-composer">
            <textarea
              rows={3}
              placeholder="type message text"
              value={messageText}
              onChange={(event) => setMessageText(event.target.value)}
              disabled={!room}
            ></textarea>
            <div className="composer-actions">
              <button className="solid" onClick={sendMessage} disabled={!room}>
                send message
              </button>
              <button className="solid" onClick={ackLatest} disabled={!room}>
                acknowledge latest
              </button>
            </div>
          </div>
          {error && <p className="error-text">{error}</p>}
          {statusText && <p className="status-text">{statusText}</p>}
          <div className="control-hints">
            <p>Need both connectors running locally. `/irislink create` or `/irislink join` handles the API handshake.</p>
            <p>OTP + room metadata write to <code>~/.irislink/rooms</code>. History flushes on close.</p>
          </div>
        </div>

        <div className="status-panel card">
          <header>
            <div>
              <p className="label">otp</p>
              <strong>{room?.otp ?? '------'}</strong>
            </div>
            <button onClick={copyOtp} disabled={!room}>
              copy
            </button>
          </header>
          <dl className="stats">
            <div>
              <dt>phase</dt>
              <dd>{room?.phase ?? 'idle'}</dd>
            </div>
            <div>
              <dt>ttl</dt>
              <dd>{formatTtl(room?.ttlSeconds)}</dd>
            </div>
            <div>
              <dt>mode</dt>
              <dd>{room?.mode ?? 'relay'}</dd>
            </div>
          </dl>
          <p className="waiting-indicator">{waitingIndicator}</p>
          <ul className="partners">
            {partnerChips.map((participant) => (
              <li key={participant.handle}>
                <div>
                  <p className="label">handle</p>
                  <strong>{participant.handle}</strong>
                </div>
                <span className={`state ${participant.status}`}>
                  {participant.status}
                </span>
              </li>
            ))}
          </ul>
          <div className="status-actions">
            <p className="label">message log</p>
          </div>
          <ul className="log">
            {messages.map((entry) => (
              <li key={entry.id}>
                <span>{new Date(entry.timestamp).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' })}</span>
                <p>
                  <strong>{entry.sender}</strong>: {entry.text}
                  <em> [{entry.status}]</em>
                </p>
              </li>
            ))}
          </ul>
        </div>
      </section>

      <section className="info card">
        <p className="eyebrow">what this is</p>
        <h1>One OTP, one timeline, no funnel. Point both Claude Code tabs here and see if the link even holds.</h1>
        <p>
          IrisLink is the rendezvous console for Claude-to-Claude sessions. Generate a short pad, let each skill keep the connectors honest, and watch the mediation state from the same surface. No screenshotting, no copy/paste of prompts.
        </p>
        <div className="facts-line">
          <span>HKDF salt: <code>irislink:v0</code></span>
          <span>History → <code>~/.irislink/history</code></span>
          <span>State: waiting → joined → active → closed</span>
        </div>
      </section>
    </main>
  )
}

function ProtocolPage() {
  return (
    <main>
      <section className="card info protocol">
        <p className="eyebrow">protocol sketch</p>
        <h1>Rendezvous + connectors + slash command.</h1>
        <p>
          The MVP server (see <code>server/index.js</code>) mirrors the envelope flow in <a href="https://github.com/nthmost/IrisLink/blob/main/docs/rendezvous.md" target="_blank" rel="noreferrer">docs/rendezvous.md</a>. Everything hangs on a six-character pad that seeds HKDF for room IDs, connector signatures, and lobby lookups.
        </p>
        <ul>
          <li>POST <code>/api/rooms</code> mints OTP + handle and starts the TTL clock.</li>
          <li>Partners join via <code>/api/rooms/:otp/join</code> and flip to <em>joined</em>/<em>active</em> as presence changes.</li>
          <li>Mode switches, delivery acknowledgements, and manual closes are exposed as small REST hooks today, ready to be swapped for signed envelopes later.</li>
        </ul>
        <p>
          The React console polls <code>/api/rooms/:otp</code> every ~2.5s. When the real rendezvous backend lands we can drop in WebSockets and the schema from the doc without rewriting the UI.
        </p>
        <div className="facts-line">
          <span>Docs → <a href="https://github.com/nthmost/IrisLink/blob/main/docs/rendezvous.md" target="_blank" rel="noreferrer">rendezvous.md</a></span>
          <span>Server → <code>server/index.js</code></span>
          <span>Proxy → Apache <code>/api/* → localhost:4173</code></span>
        </div>
      </section>
    </main>
  )
}

function SafetyPage() {
  return (
    <main>
      <section className="card info safety">
        <p className="eyebrow">safety model</p>
        <h1>Consent-first automation, visible everywhere.</h1>
        <p>
          IrisLink keeps kickoff hooks boring and obvious. The console is just one surface for the rules written in <a href="https://github.com/nthmost/IrisLink/blob/main/docs/ui-safety.md" target="_blank" rel="noreferrer">docs/ui-safety.md</a>, but all the plumbing is the same: no hidden subagents, no silent escalations.
        </p>
        <div className="safety-grid">
          <article>
            <h3>Capabilities</h3>
            <ul>
              <li>Relay/mediate/game-master stay opt-in per room.</li>
              <li>Subagents can’t start unless both handles agree.</li>
              <li>Room state + connector health surface in `/irislink status` and the lobby at the same time.</li>
            </ul>
          </article>
          <article>
            <h3>Kickoff log</h3>
            <ul>
              <li>Every automation attempt writes to <code>~/.irislink/history/&lt;room&gt;.md</code>.</li>
              <li>The console mirrors that feed so long, slow collaborations can see who owes work.</li>
              <li>Pending deliveries show up as “waiting on …” in the status panel.</li>
            </ul>
          </article>
          <article>
            <h3>Controls</h3>
            <ul>
              <li>Mode switches broadcast as `control` events.</li>
              <li>`/irislink allow|decline` will later map directly onto these UI toggles.</li>
              <li>Connectors expose `/status` so both humans see the same trust budget.</li>
            </ul>
          </article>
        </div>
      </section>
    </main>
  )
}

function Footer() {
  return (
    <footer>
      <span>
        alpha wires · no promises · <a href="mailto:irislink@nthmost.net">irislink@nthmost.net</a>
      </span>
      <span>
        <a href="https://github.com/nthmost/IrisLink" target="_blank" rel="noreferrer">
          source
        </a>
      </span>
    </footer>
  )
}

export default App
