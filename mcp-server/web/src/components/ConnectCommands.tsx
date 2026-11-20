import { useState } from 'react'
import { Eye, EyeOff } from 'lucide-react'
import { CopyBlock } from './CopyBlock'
import { WarnBox } from './ui'

export const INSTALL_CMD = 'curl -fsSL https://raw.githubusercontent.com/enowdev/enowx-rag/main/install.sh | sh'

function tokenFlag(token: string): string {
  return token ? ` --token ${token}` : ''
}

export function connectCmd(url: string, token: string): string {
  return `enowx-rag connect --remote ${url}${tokenFlag(token)}`
}

export function pushCmd(url: string, token: string): string {
  return `enowx-rag push --remote ${url}${tokenFlag(token)}`
}

export function clientMcpJson(url: string, token: string): string {
  const env: Record<string, string> = { RAG_VECTOR_STORE: 'remote', RAG_REMOTE_URL: url }
  if (token) env.RAG_REMOTE_TOKEN = token
  return JSON.stringify({ mcpServers: { 'enowx-rag': { command: 'enowx-rag', env } } }, null, 2)
}

export function directMcpJson(url: string, token: string): string {
  const entry: Record<string, unknown> = { url: `${url}/mcp` }
  if (token) entry.headers = { Authorization: `Bearer ${token}` }
  return JSON.stringify({ mcpServers: { 'enowx-rag': entry } }, null, 2)
}

interface ConnectCommandsProps {
  url: string
  token: string
  /** Show the optional push + manual sections (the wizard keeps it short). */
  full?: boolean
  /** Warn when there is no token (off when the caller already offers to create one). */
  tokenWarning?: boolean
}

/** Step-by-step commands to connect a local machine in client mode. */
export function ConnectCommands({ url, token, full = true, tokenWarning = true }: ConnectCommandsProps) {
  const [reveal, setReveal] = useState(false)
  const toggle = () => setReveal((v) => !v)
  const secret = token || undefined
  return (
    <div className="connect-steps">
      {token ? (
        <div className="reveal-bar">
          <span>Commands include your admin token.</span>
          <button type="button" className="btn small" onClick={toggle}>
            {reveal ? <EyeOff size={13} /> : <Eye size={13} />}
            {reveal ? 'Hide token' : 'Show token'}
          </button>
        </div>
      ) : tokenWarning && (
        <WarnBox title="This server has no admin token.">
          Anyone who can reach {url} can read and change your memory. Set one in Settings → Admin token (or
          <span className="mono"> RAG_ADMIN_TOKEN</span>) before exposing the server publicly.
        </WarnBox>
      )}

      <div className="cstep">
        <div className="cstep-num">1</div>
        <div className="cstep-body">
          <div className="cstep-title">Install enowx-rag on the machine</div>
          <div className="cstep-desc">Installs the enowx-rag binary. Skip if it is already installed.</div>
          <CopyBlock title="terminal" code={INSTALL_CMD} />
        </div>
      </div>

      <div className="cstep">
        <div className="cstep-num">2</div>
        <div className="cstep-body">
          <div className="cstep-title">Connect it to this server</div>
          <div className="cstep-desc">
            Writes the MCP config (client mode) for Claude Code and other detected agents. Restart your agent afterwards.
          </div>
          <CopyBlock title="terminal" code={connectCmd(url, token)} secret={secret} revealed={reveal} onToggleReveal={toggle} />
        </div>
      </div>

      {full && (
        <div className="cstep">
          <div className="cstep-num">3</div>
          <div className="cstep-body">
            <div className="cstep-title">Optional: bring existing local memory</div>
            <div className="cstep-desc">
              Uploads <span className="mono">~/.enowx-rag/rag.db</span> and imports it here, including which folder each project came from.
            </div>
            <CopyBlock title="terminal" code={pushCmd(url, token)} secret={secret} revealed={reveal} onToggleReveal={toggle} />
          </div>
        </div>
      )}

      {full && (
        <>
          <div className="sub-head">Manual MCP config (client mode)</div>
          <div className="cstep-desc">
            If you'd rather edit your agent's MCP config yourself, add this entry. It runs the local binary in client mode.
          </div>
          <CopyBlock title="mcp.json" code={clientMcpJson(url, token)} secret={secret} revealed={reveal} onToggleReveal={toggle} />

          <div className="sub-head">Alternative: direct URL (no local binary)</div>
          <div className="cstep-desc">
            Agents that support remote MCP servers can talk to <span className="mono">{url}/mcp</span> directly.
            <b> Limitation:</b> the server can't see your machine's files, so it can search existing memory but
            <b> can't index or auto-sync local folders</b>. Use client mode for coding projects.
          </div>
          <CopyBlock title="mcp.json (remote)" code={directMcpJson(url, token)} secret={secret} revealed={reveal} onToggleReveal={toggle} />
        </>
      )}
    </div>
  )
}

/** Short explanation of client mode. */
export function ClientModeExplainer() {
  return (
    <div className="flow">
      <div className="flow-step">
        <div className="flow-title">Your machine</div>
        <div className="flow-desc">Runs <span className="mono">enowx-rag</span> in client mode: scans your project folders, chunks code locally and syncs changes. Files never need to live on the server.</div>
      </div>
      <div className="flow-arrow" aria-hidden="true">→</div>
      <div className="flow-step">
        <div className="flow-title">This server</div>
        <div className="flow-desc">Embeds chunks with Voyage, stores them in the database, reranks search results and records metrics for this dashboard.</div>
      </div>
      <div className="flow-arrow" aria-hidden="true">→</div>
      <div className="flow-step">
        <div className="flow-title">Always fresh</div>
        <div className="flow-desc">Before each search the client syncs changed files automatically, so your agent never searches stale code.</div>
      </div>
    </div>
  )
}
