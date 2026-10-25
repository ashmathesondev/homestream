#!/usr/bin/env bash
# Manage the built Go API on Linux. Nginx serves the frontend separately.
set -euo pipefail

project_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
runtime_dir="$project_dir/.run"
pid_file="$runtime_dir/server.pid"
log_file="$runtime_dir/server.log"
config_file="${SERVER_CONFIG:-$project_dir/backend/config.json}"
mkdir -p -- "$runtime_dir"
# Serialize commands; do not pass this lock to the background server.
exec 9>"$runtime_dir/server.lock"
flock -x 9

# Match PID and process start time so stale files cannot target reused PIDs.
process_token() {
  local stat rest
  [[ "$1" =~ ^[0-9]+$ && -r "/proc/$1/stat" ]] || return 1
  stat=$(cat "/proc/$1/stat" 2>/dev/null) || return 1
  rest=${stat##*) }
  [[ ${rest%% *} != Z ]] || return 1
  awk '{print $20}' <<< "$rest"
}

running() {
  [[ -f "$pid_file" ]] || return 1
  read -r server_pid saved_token < "$pid_file" || return 1
  [[ -n "$saved_token" ]] || return 1
  [[ "$(process_token "$server_pid")" == "$saved_token" ]] || return 1
  kill -0 "$server_pid" 2>/dev/null
}

start() {
  if running; then
    echo "Server already running (PID $server_pid)."
    return
  fi
  rm -f -- "$pid_file"
  [[ -x "$project_dir/bin/server" ]] || {
    echo 'Build the server first: npm run build:api' >&2; return 1;
  }
  [[ -r "$config_file" ]] || {
    echo "Configuration not readable: $config_file" >&2; return 1;
  }
  cd -- "$project_dir"
  nohup "$project_dir/bin/server" -config "$config_file" >> "$log_file" 2>&1 < /dev/null 9>&- &
  server_pid=$!
  local token
  if ! token=$(process_token "$server_pid"); then
    echo "Server failed to start. See $log_file" >&2; return 1
  fi
  printf '%s %s\n' "$server_pid" "$token" > "$pid_file"
  sleep 0.5
  if ! running; then
    rm -f -- "$pid_file"
    echo "Server failed to start. See $log_file" >&2; return 1
  fi
  echo "Server started (PID $server_pid). Logs: $log_file"
}

stop() {
  if ! running; then
    rm -f -- "$pid_file"
    echo 'Server is not running.'
    return
  fi
  kill -TERM "$server_pid"
  for ((attempt=0; attempt<100; attempt++)); do
    if ! running; then
      rm -f -- "$pid_file"
      echo 'Server stopped.'
      return
    fi
    sleep 0.1
  done
  echo "Server has not stopped after 10 seconds; PID file retained. See $log_file" >&2
  return 1
}

case "${1:-}" in
  start) start ;;
  stop) stop ;;
  restart) stop; start ;;
  status)
    if running; then echo "Server running (PID $server_pid)."
    else echo 'Server is not running.'; exit 1; fi ;;
  *) echo "Usage: $0 {start|stop|restart|status}" >&2; exit 2 ;;
esac
