#!/usr/bin/env python3
"""Exercise an installed release without public DNS or ACME requests.

Foreground mode uses temporary files and high ports. --systemd is restricted to
fresh Linux CI hosts and uses the real system service and default ports 80/443.
"""

import argparse
import http.server
import json
import os
import pathlib
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time


DOMAIN = "app.proxctl.test"
CONTROL_DOMAIN = "health.proxctl.test"


def command(args, check=True):
    result = subprocess.run(args, capture_output=True, text=True, timeout=20)
    if check and result.returncode:
        raise RuntimeError(f"command failed: {args!r}\n{result.stdout}\n{result.stderr}")
    return result


def available_ports():
    with socket.socket() as first, socket.socket() as second:
        first.bind(("127.0.0.1", 0))
        second.bind(("127.0.0.1", 0))
        return first.getsockname()[1], second.getsockname()[1]


def backend(body):
    class Handler(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            encoded = body.encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(encoded)))
            self.end_headers()
            self.wfile.write(encoded)

        def log_message(self, *_):
            pass

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server, thread


def wait_for(check, description, process=None):
    deadline = time.monotonic() + 12
    while time.monotonic() < deadline:
        if process is not None and process.poll() is not None:
            raise RuntimeError(f"proxctl exited while waiting for {description}")
        if check():
            return
        time.sleep(0.2)
    raise RuntimeError(f"timed out waiting for {description}")


def verify(binary, expected_version, use_systemd):
    if command([binary, "--version"]).stdout.strip() != f"proxctl version {expected_version}":
        raise RuntimeError("installer did not install the expected release")
    if use_systemd:
        if sys.platform != "linux" or os.environ.get("CI") != "true":
            raise RuntimeError("--systemd is only for a fresh Linux CI host")
        for path in ["/etc/proxctl", "/etc/systemd/system/proxctl.service"]:
            if command(["sudo", "test", "!", "-e", path], check=False).returncode:
                raise RuntimeError(f"refusing to replace existing {path}")

    with tempfile.TemporaryDirectory(prefix="proxctl-install-check-") as scratch:
        root = pathlib.Path(scratch)
        certs = root / "certs"
        certs.mkdir(mode=0o700)
        web = root / "web"
        web.mkdir()
        (web / "index.html").write_text("proxctl-release-static")
        key, cert = root / "key.pem", root / "cert.pem"
        openssl_config = root / "openssl.cnf"
        openssl_config.write_text(f"[req]\ndistinguished_name=dn\n[dn]\n[v3_req]\nsubjectAltName=DNS:{DOMAIN},DNS:{CONTROL_DOMAIN}\n")
        command(["openssl", "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", str(key)])
        command(["openssl", "req", "-new", "-x509", "-sha256", "-key", str(key), "-days", "90", "-subj", f"/CN={DOMAIN}", "-extensions", "v3_req", "-config", str(openssl_config), "-out", str(cert)])
        cached = certs / DOMAIN
        cached.write_bytes(key.read_bytes() + cert.read_bytes())
        cached.chmod(0o600)
        control_cache = certs / CONTROL_DOMAIN
        control_cache.write_bytes(cached.read_bytes())
        control_cache.chmod(0o600)

        config_path = pathlib.Path("/etc/proxctl/config.json") if use_systemd else root / "config.json"
        cert_dir = "/etc/proxctl/certs" if use_systemd else str(certs)
        http_port, https_port = (80, 443) if use_systemd else available_ports()
        local_config = root / "config.json"
        local_config.write_text(json.dumps({"http_port": http_port, "https_port": https_port, "cert_dir": cert_dir, "rules": {}}))
        local_config.chmod(0o600)
        prefix = ["sudo"] if use_systemd else []
        cli = [*prefix, binary, "--config", str(config_path)]
        process = None
        servers = []
        service_created = False
        logs = (root / "serve.log").open("w+")
        try:
            if use_systemd:
                service_created = True
                command(["sudo", "install", "-d", "-m", "0700", "/etc/proxctl", cert_dir])
                command(["sudo", "install", "-m", "0600", str(local_config), str(config_path)])
                command(["sudo", "install", "-m", "0600", str(cached), f"{cert_dir}/{DOMAIN}"])
                command(["sudo", "install", "-m", "0600", str(control_cache), f"{cert_dir}/{CONTROL_DOMAIN}"])
            command([*cli, "static", DOMAIN, "--dir", str(web), "--spa"])
            command([*cli, "static", CONTROL_DOMAIN, "--dir", str(web), "--spa"])
            if use_systemd:
                command([*cli, "daemon", "install"])
                command([*cli, "daemon", "status"])
                command(["sudo", "systemctl", "is-enabled", "--quiet", "proxctl"])
                command(["sudo", "systemctl", "is-active", "--quiet", "proxctl"])
                if command(["sudo", "stat", "--format=%a", str(config_path)]).stdout.strip() != "600":
                    raise RuntimeError("system config permissions are not 0600")
            else:
                process = subprocess.Popen([*cli, "serve"], stdout=logs, stderr=logs)

            def fetch(path, extra=(), host=DOMAIN):
                return command(["curl", "--silent", "--show-error", "--noproxy", "*", "--max-time", "2", "--cacert", str(cert), "--resolve", f"{host}:{https_port}:127.0.0.1", "--write-out", "\n%{http_code}", *extra, f"https://{host}:{https_port}{path}"], check=False)

            def request(path, status, body=None, extra=(), host=DOMAIN):
                result = fetch(path, extra, host)
                if result.returncode:
                    return False
                response_body, response_status = result.stdout.rsplit("\n", 1)
                return response_status == str(status) and (body is None or response_body == body)

            wait_for(lambda: request("/dashboard", 200, "proxctl-release-static"), "HTTPS SPA hosting", process)
            redirect = command(["curl", "--silent", "--show-error", "--noproxy", "*", "--max-time", "2", "--dump-header", "-", "--output", os.devnull, "--resolve", f"{DOMAIN}:{http_port}:127.0.0.1", f"http://{DOMAIN}:{http_port}/dashboard?q=1"]).stdout.lower()
            expected_authority = DOMAIN if https_port == 443 else f"{DOMAIN}:{https_port}"
            if "301 moved permanently" not in redirect or f"location: https://{expected_authority}/dashboard?q=1" not in redirect:
                raise RuntimeError(f"incorrect HTTP redirect: {redirect}")

            auth = ("--user", "release:test-only")
            for body in ["proxctl-release-first-backend", "proxctl-release-second-backend"]:
                server, thread = backend(body)
                servers.append((server, thread))
                target = f"127.0.0.1:{server.server_address[1]}"
                command([*cli, "add", DOMAIN, "--to", target, "--auth", "release:test-only", "--cors"])
                wait_for(lambda: request("/api", 200, body, auth), "proxy target reload", process)
                if not request("/api", 401):
                    raise RuntimeError("Basic Auth did not protect the backend")
                preflight = ("--request", "OPTIONS", "--header", "Origin: https://frontend.test", "--header", "Access-Control-Request-Method: GET", "--header", "Access-Control-Request-Headers: authorization")
                if not request("/api", 204, extra=preflight):
                    raise RuntimeError("authenticated CORS preflight failed")

            command([*cli, "rm", DOMAIN])

            def removed():
                result = fetch("/api")
                # autocert refuses a removed SNI before routing; an established
                # TLS connection can instead reach the router's 404 response.
                rejected = result.returncode == 35 or (result.returncode == 0 and result.stdout.endswith("\n404"))
                return rejected and request("/dashboard", 200, "proxctl-release-static", host=CONTROL_DOMAIN)

            wait_for(removed, "rule removal with other domain still healthy", process)
            command([*cli, "static", DOMAIN, "--dir", str(web), "--spa"])
            wait_for(lambda: request("/dashboard", 200, "proxctl-release-static"), "restored static rule", process)
            if DOMAIN not in command([*cli, "list"]).stdout:
                raise RuntimeError("list omitted the configured domain")
            if use_systemd:
                command([*cli, "daemon", "restart"])
                wait_for(lambda: request("/dashboard", 200, "proxctl-release-static"), "systemd restart and certificate persistence")
                command([*cli, "daemon", "stop"])
                if command(["sudo", "systemctl", "is-active", "--quiet", "proxctl"], check=False).returncode == 0:
                    raise RuntimeError("daemon stop left the service active")
                command([*cli, "daemon", "start"])
                wait_for(lambda: request("/dashboard", 200, "proxctl-release-static"), "systemd start")
            elif config_path.stat().st_mode & 0o777 != 0o600:
                raise RuntimeError("config permissions are not 0600")
            print(f"PASS installed {expected_version}: static/SPA, HTTPS, redirect, proxy, target reload, Auth/CORS, removal, persistence" + (", systemd enabled/start/stop/restart" if use_systemd else ", foreground service"))
        finally:
            if process is not None:
                process.terminate()
                try:
                    process.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                    raise RuntimeError("foreground server did not stop gracefully")
                if process.returncode != 0:
                    logs.flush()
                    print((root / "serve.log").read_text(), file=sys.stderr)
                    raise RuntimeError(f"foreground server exit={process.returncode}")
            if service_created:
                journal = command(["sudo", "journalctl", "-u", "proxctl", "--no-pager", "-n", "60"], check=False)
                if sys.exc_info()[0] is not None:
                    print(journal.stdout, file=sys.stderr)
                command(["sudo", "systemctl", "disable", "--now", "proxctl"], check=False)
                command(["sudo", "rm", "-f", "/etc/systemd/system/proxctl.service"], check=False)
                command(["sudo", "systemctl", "daemon-reload"], check=False)
                command(["sudo", "rm", "-rf", "/etc/proxctl"], check=False)
            for server, thread in servers:
                server.shutdown()
                thread.join(timeout=2)
                server.server_close()
            logs.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", required=True)
    parser.add_argument("--expected-version", required=True)
    parser.add_argument("--systemd", action="store_true")
    args = parser.parse_args()
    executable = shutil.which(args.binary)
    if not executable:
        parser.error("binary does not exist")
    verify(str(pathlib.Path(executable).resolve()), args.expected_version, args.systemd)
