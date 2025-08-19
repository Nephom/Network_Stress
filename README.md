# 🚀 高效能網路測試工具 (400Gbps 優化版)

這是一套專為高速網路環境設計的 TCP 效能測試工具，特別針對 400Gbps 網路優化。採用 Mirror 模式進行雙向測試，提供精確的網路效能分析。

## 📋 功能特色

### 🔄 Mirror 雙向測試
- Client 發送數據到 Server
- Server 接收完畢後發送相同大小數據回 Client  
- 真正的雙向效能測試，完整評估網路能力

### 🌐 智慧網路管理
- **網卡自動檢測**: Server 啟動時自動掃描並顯示所有網路介面狀態
- **IP 綁定支援**: 可指定特定 IP 地址綁定，支援多網卡環境
- **連通性測試**: Client 執行前自動測試目標 IP 連通性
- **IP 地址驗證**: 自動驗證 IP 格式和可達性

### ⚡ 高效能優化
- **多執行緒**: 支援最多 64 個並行連接
- **大緩衝區**: 預設 256MB 緩衝區，可調整至更大
- **TCP 優化**: BBR 擁塞控制、TCP_NODELAY、大型 Socket 緩衝區
- **智慧傳輸**: 針對 400Gbps 網路的最佳化演算法

### 📊 詳細統計分析
- 即時效能監控
- 最大/最小/平均速度統計
- 網卡資訊自動檢測
- 完整的測試報告

### ⏰ 長時間測試支援
- `-t` 參數控制總測試時長（秒）
- 自動重複測試直到達到指定時間
- 每次測試結果獨立統計

## 🔧 快速開始

### 1. 啟動 Server（在目標測試機器上）

```bash
# 基本啟動（綁定所有網卡）
./server

# 綁定特定 IP 地址（推薦用於多網卡環境）
./server -bind 192.168.1.100 -port 9999

# 高效能模式（推薦用於 400Gbps 網路）
./server -bind 10.0.0.100 -opti -c 32 -buffer 268435456 -size 10240

# 完整參數範例
./server -bind 172.16.1.10 -opti -port 9999 -c 32 -buffer 268435456 -size 10240 -debug
```

### 2. 啟動 Client（在測試發起機器上）

```bash
# 基本測試（使用 -c 參數，類似 iperf）
./client -c 192.168.1.100

# 指定埠號和測試時長
./client -c 192.168.1.100 -port 9999 -t 300

# 長時間高效能測試（24小時）
./client -c 10.0.0.100 -opti -t 86400 -P 32 -buffer 268435456 -size 10240

# 短時間測試範例（5分鐘）
./client -c 172.16.1.10 -opti -t 300 -P 16 -size 1024 -debug
```

## 📖 參數說明

### Server 參數
| 參數 | 預設值 | 說明 |
|------|--------|------|
| `-port` | 9999 | 監聽埠號 |
| `-bind` | "" | 綁定的 IP 地址（空值表示綁定所有網卡）|
| `-c` | 16 | 最大並行連接數 (1-64) |
| `-buffer` | 268435456 | 緩衝區大小（位元組）|
| `-size` | 10240 | 每次傳輸大小（MB）|
| `-opti` | false | 啟用 TCP 優化和 BBR |
| `-debug` | false | 詳細日誌輸出 |

### Client 參數
| 參數 | 預設值 | 說明 |
|------|--------|------|
| `-c` | "" | **目標服務器 IP 地址（類似 iperf -c）** |
| `-host` | localhost | Server 主機位址（已棄用，建議使用 -c）|
| `-port` | 9999 | Server 埠號 |
| `-t` | 60 | 總測試時長（秒）|
| `-P` | 16 | 並行連接數 (1-64) |
| `-buffer` | 268435456 | 緩衝區大小（位元組）|
| `-size` | 10240 | 每次傳輸大小（MB）|
| `-opti` | false | 啟用 TCP 優化和 BBR |
| `-debug` | false | 詳細日誌輸出 |

## 🎯 效能調優建議

### 針對不同網路環境的建議設定

#### 💎 400Gbps 超高速網路
```bash
# Server（綁定高速網卡）
./server -bind 10.0.0.100 -opti -c 32 -buffer 536870912 -size 20480

# Client (24小時測試)
./client -c 10.0.0.100 -opti -t 86400 -P 32 -buffer 536870912 -size 20480
```

#### 🚀 100Gbps 高速網路
```bash
# Server（綁定指定網卡）
./server -bind 192.168.1.100 -opti -c 16 -buffer 268435456 -size 10240

# Client (4小時測試)
./client -c 192.168.1.100 -opti -t 14400 -P 16 -buffer 268435456 -size 10240
```

#### ⚡ 10Gbps 一般網路
```bash
# Server
./server -bind 172.16.1.10 -opti -c 8 -buffer 134217728 -size 1024

# Client (1小時測試)
./client -c 172.16.1.10 -opti -t 3600 -P 8 -buffer 134217728 -size 1024
```

### 🐧 Linux 系統優化

當啟用 `-opti` 參數時，程式會自動進行以下優化：

1. ✅ 啟用 BBR 擁塞控制演算法（需要 Linux 4.9+）
2. ✅ 設定 64MB Socket 緩衝區
3. ✅ 啟用 TCP_NODELAY
4. ✅ 配置 TCP KeepAlive

#### 手動系統調優（需要 root 權限）
```bash
# 增加系統緩衝區限制
echo 'net.core.rmem_max = 1073741824' >> /etc/sysctl.conf
echo 'net.core.wmem_max = 1073741824' >> /etc/sysctl.conf
echo 'net.ipv4.tcp_rmem = 4096 87380 1073741824' >> /etc/sysctl.conf
echo 'net.ipv4.tcp_wmem = 4096 65536 1073741824' >> /etc/sysctl.conf

# 啟用 BBR
echo 'net.core.default_qdisc = fq' >> /etc/sysctl.conf
echo 'net.ipv4.tcp_congestion_control = bbr' >> /etc/sysctl.conf

# 應用設定
sysctl -p
```

## 📊 測試報告範例

```
================================================================================
                        測試總結報告
================================================================================
網卡名稱:          eth0
網卡速度:          100000 Mbps
測試時長:          300.50 秒
測試檔案大小:      10240 MB
傳輸次數:          45
總傳輸數據:        902.34 GB
最大傳輸效能:      98.45 Gbps
最小傳輸效能:      94.23 Gbps
平均傳輸效能:      96.78 Gbps
================================================================================

詳細傳輸記錄:
序號    開始時間        持續時間    發送(MB)    接收(MB)    發送速度    接收速度    整體速度
1       14:30:15       6.45s       10240.00    10240.00    12.65       12.65       96.23
2       14:30:23       6.38s       10240.00    10240.00    12.78       12.78       97.45
...
```

## 🌐 網路介面管理

### Server 網卡檢測功能
Server 啟動時會自動檢測並顯示所有網路介面狀態：

```
Network Interface Status:
========================
Active interfaces with valid IP addresses:
  ✓ eth0: 192.168.1.100
  ✓ eth1: 10.0.0.100
Interfaces without valid IP addresses:
  ✗ eth2 (no valid IP configured)
  ✗ eth3 (no valid IP configured)
Please configure IP addresses for the above interfaces if needed.
```

### 多網卡環境使用
```bash
# 查看可用網卡（啟動 server 即可看到）
./server

# 綁定到特定網卡的 IP
./server -bind 192.168.1.100    # 綁定到 eth0
./server -bind 10.0.0.100       # 綁定到 eth1

# 客戶端連接到對應 IP
./client -c 192.168.1.100       # 連接到 eth0
./client -c 10.0.0.100          # 連接到 eth1
```

### Client 連通性測試
Client 執行前會自動測試連通性：

```
Testing connectivity to 192.168.1.100:9999...
✓ Connectivity test successful to 192.168.1.100:9999
```

## ⚠️ 重要注意事項

### 系統需求
- **作業系統**: Linux x86_64
- **核心版本**: 4.9+ (BBR 支援)
- **記憶體**: 建議 8GB+ (大緩衝區使用)
- **網路**: 支援高速網路介面

### 使用建議
- 🔒 **測試環境**: 建議在專用測試環境中執行
- 💻 **資源消耗**: 高速測試會消耗大量 CPU 和記憶體
- 🔐 **權限需求**: TCP 優化需要適當的系統權限
- 🌐 **防火牆**: 確保測試埠號未被阻擋
- 🖥️ **多網卡**: 使用 `-bind` 參數指定特定網卡進行測試

### 故障排除
1. **連接失敗**: 檢查防火牆和網路連通性，使用 `-c` 參數指定正確 IP
2. **網卡綁定失敗**: 確認指定的 IP 地址存在於本機網卡上
3. **連通性測試失敗**: 檢查目標 IP 是否可達，防火牆是否開放對應埠號
4. **效能不佳**: 嘗試啟用 `-opti` 參數和系統調優
5. **記憶體不足**: 減少 `-buffer` 和 `-c` 參數
6. **BBR 無法啟用**: 確認 Linux 核心版本 4.9+

## 📄 授權

本工具為開源軟體，歡迎在遵循相關授權條款下使用和修改。

---

🔗 **聯絡資訊**: 如有問題或建議，歡迎回饋。