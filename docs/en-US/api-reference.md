# API Reference

[中文](../zh-CN/api-reference.md)

CyberStrikeAI exposes built-in OpenAPI docs:

```text
/api-docs
GET /api/openapi/spec
```

The OpenAPI spec is protected to avoid exposing the API surface to unauthenticated users.

## Authentication

Login:

```http
POST /api/auth/login
Content-Type: application/json

{"password":"your-password"}
```

The auth middleware accepts token from:

1. `Authorization: Bearer <token>`
2. `Authorization: <token>`
3. `?token=<token>`
4. `auth_token` cookie

Prefer `Authorization: Bearer` for scripts. Query tokens can leak through logs.

## Agent APIs

Single-agent:

- `POST /api/eino-agent`
- `POST /api/eino-agent/stream`

Multi-agent:

- `POST /api/multi-agent`
- `POST /api/multi-agent/stream`

`orchestration` may be `deep`, `plan_execute`, or `supervisor`.

Common request body fields:

| Field | Meaning |
| --- | --- |
| `message` | User message, required. |
| `conversationId` | Continue an existing conversation; empty creates a new one. |
| `projectId` | Project for a new conversation; empty may follow `config.project.default_project_id`. |
| `role` | Use a named role. |
| `aiChannelId` | Select a channel from `ai.channels`; empty follows `ai.default_channel`. |
| `reasoning` | Per-session reasoning override, controlled by the channel's `reasoning.allow_client_reasoning`. |
| `hitl` | Per-session human-in-the-loop settings. |

## SSE Notes

Streaming endpoints are long-lived. Clients should:

- handle `error` events;
- wait for `done`;
- avoid blindly replaying destructive requests;
- disable proxy buffering;
- pass `conversationId` when continuing a conversation.

## File Management Sources

The file management page and `GET /api/chat-uploads` group conversation-related files by source. Directory names still use project IDs or conversation IDs for stability, while the UI prefers project names or conversation titles and keeps the full ID available in tooltips or copied paths.

| Source | `source` | Typical directory | Meaning | Mutability |
| --- | --- | --- | --- | --- |
| Workspace files | `workspace` | `tmp/workspace/projects/<projectId>/...`, `tmp/workspace/conversations/<conversationId>/...` | The Agent workspace for downloaded files, analysis scripts, intermediate results, and generated CSV/XLSX/Markdown files. If an AI-generated file is missing from the UI, check this source first. | Read-only listing; supports copy path, download, and export. |
| Conversation artifacts | `conversation_artifact` | `data/conversation_artifacts/<conversationId>/...` | Conversation-scoped deliverables or archived artifacts such as summaries, reports, or middleware-generated artifacts. | Read-only listing; supports copy path, download, and export. |
| Tool outputs | `reduction` | `tmp/reduction/projects/<projectId>/...`, `tmp/reduction/conversations/<conversationId>/...` | Persisted full tool outputs, scan raw data, or outputs saved before truncation. Useful for reviewing long command or scan results. | Read-only listing; supports copy path, download, and export. |
| Chat uploads | `upload` | `chat_uploads/<date>/<conversationId>/...` | Files manually uploaded in chat or from the file management page. Copy the server absolute path into chat when the AI should reference a file. | Supports upload, mkdir, text edit, rename, delete, copy path, download, and export. |

Related endpoints:

- `GET /api/chat-uploads`: list files filtered by source, project, conversation, or filename.
- `GET /api/chat-uploads/path`: resolve a file-management relative path or internal virtual path to a server absolute path for copy actions.
- `GET /api/chat-uploads/download`: download a file.
- `GET /api/chat-uploads/export`: export the current filtered result as a ZIP.
- `POST /api/chat-uploads`: upload into the chat uploads directory.

## Asset Management and Bulk Import


## Stability Tiers

| API type | Stability | Recommendation |
| --- | --- | --- |
| `/api/auth/*` | high | safe to integrate |
| `/api/eino-agent*` | high | preferred chat entry |
| `/api/openapi/spec` | high | client generation |
| `/api/config*` | medium | admin automation only |
| `/api/c2/*`, `/api/webshell/*` | medium | high-risk, restrict access |
| frontend private calls | low | avoid plugin dependency |

## Common Areas

- Conversations: `/api/conversations`
- Projects: `/api/projects`
- Vulnerabilities: `/api/vulnerabilities`
- Knowledge: `/api/knowledge/*`
- Roles: `/api/roles`
- Skills: `/api/skills`
- External MCP: `/api/external-mcp`
- Monitoring: `/api/monitor`
- Audit: `/api/audit`
- C2: `/api/c2`
- WebShell: `/api/webshell`

## Curl Example

```bash
curl -k https://127.0.0.1:8080/api/conversations \
  -H "Authorization: Bearer <token>"
```

```bash
curl -k https://127.0.0.1:8080/api/eino-agent \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/json" \
  -d '{"message":"Run authorized basic recon against 127.0.0.1; avoid high-risk actions."}'
```

## Source Anchors

- Routes: `internal/app/app.go`
- Auth middleware: `internal/security/auth_middleware.go`
- OpenAPI: `internal/handler/openapi.go`
- Single-agent: `internal/handler/eino_single_agent.go`
- Multi-agent: `internal/handler/multi_agent.go`
