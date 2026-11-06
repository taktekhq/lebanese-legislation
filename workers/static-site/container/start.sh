#!/bin/sh
exec > /tmp/start.log 2>&1
set -u
report() {
  echo "start: $(date -u +%T) $1"; ls -la "$PGDATA" | head -5; df -h / /tmp; free -m; id
  exec perl -MIO::Socket::INET -e '$s=IO::Socket::INET->new(LocalPort=>8080,Listen=>5,ReuseAddr=>1) or die $!; while($c=$s->accept){local $/; open F,"/tmp/start.log"; $b=<F>; close F; print $c "HTTP/1.0 503 Service Unavailable\r\nContent-Type: text/plain\r\nContent-Length: ".length($b)."\r\n\r\n".$b; close $c}'
}
perl -MIO::Socket::INET -e '$s=IO::Socket::INET->new(LocalPort=>8081,Listen=>5,ReuseAddr=>1) or die $!; while($c=$s->accept){local $/; open F,"/tmp/start.log"; $b=<F>; close F; print $c "HTTP/1.0 200 OK\r\nContent-Type: text/plain\r\nContent-Length: ".length($b)."\r\n\r\n".$b; close $c}' &
export LEBLEG_VECTOR_DSN="${LEBLEG_VECTOR_DSN:-host=127.0.0.1 user=lebleg_web dbname=lebleg_search}"
echo "start: $(date -u +%T) postgres starting"
ls -ld /var/run /run /var/run/postgresql 2>&1 || true
gosu postgres postgres -D "$PGDATA" -c listen_addresses=127.0.0.1 -c unix_socket_directories=/tmp -c shared_buffers=1GB -c max_connections=50 -c default_transaction_read_only=on -c jit=off &
PG=$!
# A stop from Cloudflare (deploy, move, sleep) is not a failure: stop both and exit.
trap 'echo "start: $(date -u +%T) stop signal"; kill -TERM ${WEB:-} $PG 2>/dev/null; wait; exit 0' TERM INT
i=0
until pg_isready -h 127.0.0.1 -U lebleg_web -d lebleg -q; do
  kill -0 "$PG" 2>/dev/null || report "postgres exited"
  i=$((i+1)); [ "$i" -lt 600 ] || report "postgres not ready after 300 s"
  [ $((i % 15)) -eq 0 ] && echo "start: waiting for postgres, $i s"
  sleep 1
done
echo "start: $(date -u +%T) postgres ready after $i s"
install -d -o 65532 -g 65532 /tmp/lebleg-events
gosu 65532:65532 /usr/local/bin/lebleg-web -addr 0.0.0.0:8080 -events /tmp/lebleg-events -db "host=127.0.0.1 user=lebleg_web dbname=lebleg" &
WEB=$!
wait $WEB
code=$?
[ "$code" -eq 0 ] && exit 0
report "web server exited with $code"
