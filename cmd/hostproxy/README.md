# hostproxy

`hostproxy` is a small SOCKS5 CONNECT proxy for the host network namespace.
It lets bridge-network containers reach services bound to the host loopback.

## Usage

```bash
hostproxy -listen 0.0.0.0:11080
```

In an LLM profile inside the `ghoul` container, keep the Base URL unchanged:

```text
http://127.0.0.1:17863/v1
```

and set the profile proxy to:

```text
socks5://host.docker.internal:11080
```

The default target allowlist is loopback-only (`127.0.0.0/8,::1/128`). The
default source allowlist covers loopback and Docker bridge addresses. Override
the flags only for trusted deployments:

```bash
hostproxy \
  -listen 0.0.0.0:11080 \
  -allow-source 127.0.0.1/32,172.16.0.0/12,::1/128 \
  -allow-target 127.0.0.0/8,::1/128
```

The compose service uses `restart: unless-stopped`, so it starts with the
compose project and after the WSL Docker daemon starts.
