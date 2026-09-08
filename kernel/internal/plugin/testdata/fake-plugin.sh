#!/bin/sh
# fake 插件：用于 kernel/internal/plugin 的客户端集成测试。
# 行为：initialize/snapshot/fetchFile 正常应答；fetchFile 创建目标文件（内容 "hi"）；
#       方法名含 "timeout" 前缀的调用会 sleep 3s 后再响应（用于超时测试）。
while IFS= read -r line; do
  id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9]*\).*/\1/p')
  method=$(printf '%s' "$line" | sed -n 's/.*"method":"\([^"]*\)".*/\1/p')
  case "$method" in
    initialize)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"name":"fake","version":"0.0.1"}}\n' "$id" ;;
    snapshot)
      printf '{"jsonrpc":"2.0","id":%s,"result":{"entries":[{"path":"a.txt","fingerprint":"h1","size":2}],"manifestFingerprint":"m1"}}\n' "$id" ;;
    fetchFile)
      dest=$(printf '%s' "$line" | sed -n 's/.*"destPath":"\([^"]*\)".*/\1/p')
      mkdir -p "$(dirname "$dest")"
      printf 'hi' > "$dest"
      printf '{"jsonrpc":"2.0","id":%s,"result":{"size":2}}\n' "$id" ;;
    fail)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32000,"message":"source unreachable"}}\n' "$id" ;;
    timeout*)
      sleep 3
      printf '{"jsonrpc":"2.0","id":%s,"result":{}}\n' "$id" ;;
    *)
      printf '{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"not found"}}\n' "$id" ;;
  esac
done
