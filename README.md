# KubeSphere v3.3 MCP Server

A [Model Context Protocol](https://modelcontextprotocol.io/introduction) server that reads KubeSphere **v3.3** through its `/kapis` API. It is a fork of [kubesphere/ks-mcp-server](https://github.com/kubesphere/ks-mcp-server), which targets KubeSphere 4.x APIs (`iam`/`tenant` `v1beta1`, `application` `v2`, extensions) that a v3.3 platform does not serve.

Changes from upstream:

- Member clusters are addressed as `/kapis/clusters/{cluster}/...`. The 4.x form `/clusters/{cluster}/kapis/...` gets the console's HTML page from a v3.3 console.
- Uses the v3.3 API groups: `iam.kubesphere.io/v1alpha2`, `tenant.kubesphere.io/v1alpha2` and `v1alpha3`, `resources.kubesphere.io/v1alpha3`, `openpitrix.io/v1`.
- `list_extensions` and `list_permissions` are removed (extensions and role templates are 4.x features).
- `list_secrets` never returns secret values (see below). `list_clusters` and `get_cluster` drop `spec.connection.token` and `kubeconfig`.
- Signs in with a browser session cookie file, or a bearer token. The username/password grant is removed.
- `limit` and `page` take effect (upstream ignored them because JSON numbers arrive as floats).

All tools are read-only GET requests.

## Build

```bash
go build -o mcp-kubesphere ./cmd
mcp-kubesphere stdio   # or: mcp-kubesphere sse --port 3000
```

## Authentication

### Browser session file (SSO consoles)

For a console that only accepts a browser login, for example CAS or OIDC single sign-on, point the server at an origin-keyed session file that another process keeps current:

| Variable | Meaning |
|---|---|
| `KS_URL` | Console or ks-apiserver address, e.g. `https://ks.example.com`. Overrides `--ks-apiserver`. |
| `KS_COOKIE_FILE` | Absolute path of the session file. |
| `KS_COOKIE_REFRESH_COMMAND` | Optional JSON array, e.g. `["mytool","refresh","kubesphere"]`. It runs without a shell and must exit 0 after rewriting the file. |

The file uses the same format as mcp-grafana's `GRAFANA_COOKIE_FILE` ([contract](https://github.com/duylvh/mcp-grafana#browser-session-cookie-file)):

```json
{"sessions": {"https://ks.example.com": {"value": "token=<console access token>", "expires": 1800000000, "updatedAt": 1800000000}}}
```

- Store only the `token` cookie. With `refreshToken` and `expire` also present, the console tries its own refresh and answers an invalid token with 400 instead of 401.
- The file is read on every request. It must be a regular file, not a symlink, and not readable by group or others (`chmod 600`). Errors never quote it.
- The cookie is only sent to the `KS_URL` origin. Redirects are never followed.
- With a refresh command, a missing or expiring session, a 401, or a redirect to `/login` or to another origin (the SSO server) runs the command once. The request is then retried once. Concurrent requests share one run. After a failed run the command waits 60 seconds before it runs again.
- An HTML page served with 200 for a `/kapis` path is reported as a wrong address, not as an expired session, and does not trigger a refresh.

### Bearer token

Without `KS_COOKIE_FILE`, pass `--ksconfig` with a kubeconfig-format file whose user holds a `token`. `--ks-apiserver` overrides its server address. `KUBESPHERE_CONTEXT` selects the context (default `kubesphere`).

## Tools

| Area | Tools |
|---|---|
| Users and roles | `list_users`, `get_user`, `list_roles`, `get_role` (levels: global, cluster, workspace, namespace) |
| Workspaces | `list_workspaces`, `get_workspace`, `list_workspace_members`, `get_workspace_quota`, `list_project_members` |
| App store (OpenPitrix) | `list_application_repos`, `list_applications`, `get_application`, `get_application_version` |
| Clusters | `list_clusters`, `get_cluster`, `get_cluster_tags` (built from cluster labels), `list_cluster_members`, `list_projects` |
| Cluster resources | `list_nodes`, `list_customresourcedefinitions`, `list_persistentvolumes`, `list_storageclasses` |
| Project resources | `list_deployments`, `list_statefulsets`, `list_daemonsets`, `list_jobs`, `list_cronjobs`, `list_pods`, `list_services`, `list_ingresses`, `list_secrets`, `list_configmaps`, `list_serviceaccounts`, `list_persistentvolumeclaims` |

Most tools take a `cluster` argument. A workspace's projects live on the clusters in `get_workspace` → `spec.placement.clusters`.

A 403 from KubeSphere is passed through unchanged. It means the signed-in user lacks that permission, for example for platform roles, CRDs or persistent volumes.

### What is and is not redacted

- `list_secrets` rebuilds each secret from an allowlist: its identity, type and labels, KubeSphere's display annotations, and `dataKeys` (the sorted key names). Values, other annotations and managed fields are never returned. A response of any other shape is withheld.
- Other tools return what KubeSphere returns, as `kubectl get -o json` would. This includes container `env` values in workload lists and Helm values in `get_application`.

## Tests

```bash
go test -race ./...
```
