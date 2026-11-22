# OMC QBS Relay

CLIProxyAPI / OMC plugin for `qbs-relay-kit`.

It registers multiple QBS sessions to a running `qbs-upstream` pool and forwards Anthropic/OpenAI requests to that pool. The upstream owns least-busy account selection and WebSocket connections.

## Config

```yaml
upstream_base_url: http://127.0.0.1:19100
admin_secret: change-me
api_key: sk-qbs-change-me
timeout_sec: 120
register_on_auth: true
```

## Auth

Use the `qbs-relay` login entry and paste one session JSON per account:

```json
{
  "account_id": "722...",
  "jwt": "aoa...",
  "ws_host": "qbs-xxx.plato.ai.aws.dev",
  "outpost_id": "",
  "usable_until": 1795280628,
  "email": "user@example.com"
}
```

Each account becomes a separate CPA auth file. On successful auth, the plugin calls `/admin/token` so the upstream pool can rotate between all registered accounts.

## Notes from magpie-aws-quick

The auth model follows `httpdone/magpie-aws-quick`:

- browser OAuth and desktop local credentials are the two supported session sources;
- sessions contain `jwt`, `ws_host`, refresh data and identity metadata;
- model ids are Quick agent tiers: `fast`, `balanced`, `smart`, `advanced`;
- `X-Magpie-Effort`/`thinking_effort` semantics are reserved for the future native bridge.

In `bridge_mode: upstream`, the plugin does not open the Quick WebSocket itself. It registers sessions into `qbs-upstream`, which owns the WebSocket pool, least-busy account selection and thread reuse.
