# 🚀 使用指南 - 高效能網路測試工具

## 📦 檔案說明

- `server-linux-x64` - Linux x86_64 伺服器端程式
- `client-linux-x64` - Linux x86_64 客戶端程式
- `README.md` - 完整使用說明文件

## ⚡ 快速測試 (5 分鐘)

### 1. 準備兩台 Linux 機器
- **機器A**: 運行 server (IP: 192.168.1.100)
- **機器B**: 運行 client (IP: 192.168.1.101)

### 2. 啟動 Server (在機器A上)
```bash
# 給執行權限
chmod +x server-linux-x64

# 基本啟動
./server-linux-x64

# 或高效能模式
./server-linux-x64 -opti -c 16 -size 1024
```

### 3. 啟動 Client (在機器B上)
```bash
# 給執行權限
chmod +x client-linux-x64

# 5分鐘測試
./client-linux-x64 -host 192.168.1.100 -t 300

# 高效能測試
./client-linux-x64 -host 192.168.1.100 -opti -t 300 -c 16 -size 1024
```

## 🎯 常用測試場景

### 場景1: 快速連通性測試 (30秒)
```bash
# Server
./server-linux-x64 -c 4 -size 100

# Client
./client-linux-x64 -host <server_ip> -t 30 -c 4 -size 100
```

### 場景2: 1小時效能測試
```bash
# Server
./server-linux-x64 -opti -c 16 -size 2048

# Client
./client-linux-x64 -host <server_ip> -opti -t 3600 -c 16 -size 2048
```

### 場景3: 24小時穩定性測試
```bash
# Server
./server-linux-x64 -opti -c 32 -size 10240

# Client (建議用 nohup 背景執行)
nohup ./client-linux-x64 -host <server_ip> -opti -t 86400 -c 32 -size 10240 > test_24h.log 2>&1 &
```

### 場景4: 超高速網路測試 (400Gbps)
```bash
# Server (需要 root 權限以啟用系統優化)
sudo ./server-linux-x64 -opti -c 64 -buffer 536870912 -size 20480

# Client
./client-linux-x64 -host <server_ip> -opti -t 7200 -c 64 -buffer 536870912 -size 20480
```

## 📋 參數快速參考

| 參數 | 用途 | 建議值 |
|------|------|--------|
| `-c` | 並行連接數 | 一般: 8-16, 高速: 32-64 |
| `-size` | 傳輸大小(MB) | 一般: 1024-2048, 高速: 10240+ |
| `-buffer` | 緩衝區(bytes) | 一般: 134217728, 高速: 536870912 |
| `-t` | 測試時長(秒) | 快速: 60-300, 長期: 3600+ |
| `-opti` | TCP優化 | 高速網路必須啟用 |

## 🔧 故障排除

### 連接失敗
```bash
# 檢查防火牆
sudo iptables -L | grep 9999
sudo ufw status

# 檢查埠號占用
netstat -tulpn | grep 9999
```

### 效能不佳
```bash
# 檢查 BBR 是否啟用
cat /proc/sys/net/ipv4/tcp_congestion_control

# 檢查網卡速度
ethtool eth0 | grep Speed
```

### 記憶體不足
- 減少 `-buffer` 參數 (例如: 67108864 = 64MB)
- 減少 `-c` 參數 (例如: 8)
- 減少 `-size` 參數 (例如: 512MB)

## 📊 理解測試結果

測試完成後會顯示類似以下報告：
```
================================================================================
                        測試總結報告
================================================================================
網卡名稱:          eth0
網卡速度:          100000 Mbps
測試時長:          300.50 秒
測試檔案大小:      1024 MB
傳輸次數:          45
總傳輸數據:        90.00 GB
最大傳輸效能:      98.45 Gbps    <- 網路峰值效能
最小傳輸效能:      94.23 Gbps    <- 網路最低效能
平均傳輸效能:      96.78 Gbps    <- 網路平均效能
================================================================================
```

### 效能指標說明
- **整體速度**: 雙向傳輸的總合速度
- **發送速度**: Client 到 Server 的單向速度
- **接收速度**: Server 到 Client 的單向速度
- **傳輸次數**: 在測試時間內完成的測試輪數

## 🚨 重要提醒

1. **測試環境**: 建議在專用測試環境執行，避免影響生產環境
2. **資源消耗**: 高速測試會大量消耗 CPU 和記憶體
3. **網路負載**: 測試會產生大量網路流量
4. **權限需求**: 某些優化功能需要 root 權限

---

💡 **提示**: 首次使用建議先進行 30 秒的快速測試，確認連通性後再進行長時間測試。