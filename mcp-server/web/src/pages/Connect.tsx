import { getToken } from '../lib/api'
import { ClientModeExplainer, ConnectCommands } from '../components/ConnectCommands'

export function Connect() {
  const url = window.location.origin
  const token = getToken()
  return (
    <>
      <div className="page-head">
        <h1>Connect</h1>
        <span className="id mono">{url}</span>
      </div>

      <section className="panel">
        <div className="panel-head">
          <h2>How client mode works</h2>
        </div>
        <div className="panel-body">
          <ClientModeExplainer />
        </div>
      </section>

      <section className="panel">
        <div className="panel-head">
          <h2>Connect a machine</h2>
          <span className="hint">run these on your laptop / workstation</span>
        </div>
        <div className="panel-body">
          <ConnectCommands url={url} token={token} />
        </div>
      </section>
    </>
  )
}
