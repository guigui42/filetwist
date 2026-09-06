# Reverse proxy, TLS, and authentication

Filetwist has no built-in authentication or TLS. If access extends beyond a
trusted local network, authenticate at an HTTPS reverse proxy and make the
backend reachable only by that proxy. All authenticated users still share all
jobs. Do not use it as a public multi-tenant service.

## Host-local nginx example

This example assumes nginx on the Docker host, a DNS name and TLS certificate
you control, and `htpasswd` installed. Replace `convert.example.com` and the
certificate paths. Create the password file once:

```sh
sudo htpasswd -c /etc/nginx/filetwist-users.htpasswd household
```

The command prompts for a password. `-c` creates or overwrites the file; omit
it when adding further users. Restrict file access while allowing the nginx
worker to read it. Do not commit password files or certificates.

In your Compose `.env`, set:

```dotenv
FILETWIST_IMAGE=filetwist:local
FILETWIST_PORT=127.0.0.1:8080
FILETWIST_DIAGNOSTICS=disabled
FILETWIST_MAX_UPLOAD_SIZE=4GiB
```

Start with the [CPU or VA-API deployment](packaging.md). Do not expose port
8080 on a LAN or public interface alongside the proxy, which would bypass
authentication. For a containerized proxy instead, use a private Docker
network without a published backend port.

Place this configuration in nginx's `http` context:

```nginx
limit_req_zone $binary_remote_addr zone=filetwist_per_ip:10m rate=10r/s;

server {
    listen 443 ssl;
    server_name convert.example.com;
    ssl_certificate /etc/letsencrypt/live/convert.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/convert.example.com/privkey.pem;

    auth_basic "Filetwist";
    auth_basic_user_file /etc/nginx/filetwist-users.htpasswd;
    client_max_body_size 5g;
    client_body_timeout 60s;
    send_timeout 10m;

    location / {
        limit_req zone=filetwist_per_ip burst=20 nodelay;
        proxy_http_version 1.1;
        proxy_set_header Host $http_host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header Forwarded "";
        proxy_set_header Authorization "";
        proxy_request_buffering off;
        proxy_buffering off;
        proxy_read_timeout 10m;
        proxy_send_timeout 10m;
        proxy_pass http://127.0.0.1:8080;
    }
}
```

Check nginx configuration with `sudo nginx -t` before applying it. HTTPS is
required for Basic Authentication. An existing SSO gateway can replace Basic
Authentication, but it must protect every application route.

The 5 GiB proxy body limit leaves room for multipart framing around the
application's 4 GiB file-data limit. Change both together. Buffering is off so
uploads and downloads stream. The nginx I/O timeouts are inactivity windows,
not whole-transfer limits; adjust them for expected storage/network speeds and
post-upload probing before the first response byte.

Preserve the original `Host`, `Origin`, and `Sec-Fetch-Site` headers. Filetwist
checks browser mutation origins; stripping those headers is not a fix for
rejected uploads. The example intentionally overwrites client-supplied
forwarding headers for a single host-local proxy.

## Optional subpath

For `https://convert.example.com/convert/`, add a local Compose override:

```yaml
services:
  filetwist:
    environment:
      WEBROOT: /convert
```

Load that file with `-f` after the base and CPU/VA-API files. Replace nginx's
`location /` with `location /convert/`, retaining all of its proxy settings,
and add `location / { return 404; }`. Use the trailing slash in the browser.
Keep `proxy_pass` without a trailing URI so nginx preserves `/convert/`.
Do not add a prefix-stripping rule. The container healthcheck still calls
`/healthz` at the server root; the proxy need not expose that endpoint.

## Diagnostics and forwarded addresses

Keep `DIAGNOSTICS=disabled` for proxy deployments unless you deliberately need
it. The default `local` policy permits only loopback clients, not every request
from a local proxy. Forwarded headers from an untrusted peer make the client
address untrusted, so local diagnostics are denied.

`TRUSTED_PROXIES=none` trusts no proxy. To resolve proxied client addresses,
configure only the actual proxy peer address/CIDR. `loopback` is appropriate
only if the application's peer is loopback; a host proxy reaching a Docker
published port can appear as a bridge/gateway address instead. Do not trust all
private networks. Compose uses `FILETWIST_TRUSTED_PROXIES` for this setting.

For a trusted peer, Filetwist reads `X-Forwarded-For` from right to left and
selects the first untrusted address. RFC 7239 `Forwarded` alone is not
supported. Remote clients remain remote. `DIAGNOSTICS=enabled` exposes the
endpoint to all callers and should only be used behind deliberate access
control. Proxy trust is not authentication.

References: nginx [Basic Authentication](https://nginx.org/en/docs/http/ngx_http_auth_basic_module.html),
[request limiting](https://nginx.org/en/docs/http/ngx_http_limit_req_module.html),
[body limits](https://nginx.org/en/docs/http/ngx_http_core_module.html#client_max_body_size),
and [proxy buffering/timeouts](https://nginx.org/en/docs/http/ngx_http_proxy_module.html).
