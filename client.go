package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// 針對400Gbps網路優化的參數
	DefaultBufferSize    = 256 * 1024 * 1024 // 256MB buffer
	DefaultTransferSize  = 10 * 1024 * 1024 * 1024 // 10GB per transfer
	DefaultPort          = 9999
	DefaultConnections   = 16 // 並行連接數
	MaxConnections       = 64
	SocketBufferSize     = 64 * 1024 * 1024 // 64MB socket buffer
)

type ClientConfig struct {
	ServerHost    string
	Port          int
	Connections   int
	BufferSize    int
	Optimize      bool
	Debug         bool
	TestDuration  int // 總測試時長（秒）
}

type TransferStats struct {
	StartTime         time.Time
	EndTime           time.Time
	BytesSent         int64
	BytesReceived     int64
	Duration          time.Duration
	SendThroughput    float64 // Gbps
	ReceiveThroughput float64 // Gbps
	OverallThroughput float64 // Gbps
	ConnectionCount   int
}

type TestSession struct {
	mu                sync.RWMutex
	Stats             []TransferStats
	TotalTests        int
	TotalBytes        int64
	TotalDuration     time.Duration
	MaxThroughput     float64
	MinThroughput     float64
	AvgThroughput     float64
	NetworkInterface  string
	InterfaceSpeed    string
	TransferSizeMB    int
}

func (ts *TestSession) AddTransferStat(stat TransferStats) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	
	ts.Stats = append(ts.Stats, stat)
	ts.TotalTests++
	ts.TotalBytes += stat.BytesSent + stat.BytesReceived
	ts.TotalDuration += stat.Duration
	
	// 更新最大最小值（基於整體吞吐量）
	if ts.MaxThroughput == 0 || stat.OverallThroughput > ts.MaxThroughput {
		ts.MaxThroughput = stat.OverallThroughput
	}
	if ts.MinThroughput == 0 || stat.OverallThroughput < ts.MinThroughput {
		ts.MinThroughput = stat.OverallThroughput
	}
	
	// 計算平均值
	var total float64
	for _, s := range ts.Stats {
		total += s.OverallThroughput
	}
	ts.AvgThroughput = total / float64(len(ts.Stats))
}

func (ts *TestSession) PrintSummary() {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("                        測試總結報告")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("網卡名稱:          %s\n", ts.NetworkInterface)
	fmt.Printf("網卡速度:          %s\n", ts.InterfaceSpeed)
	fmt.Printf("測試時長:          %.2f 秒\n", ts.TotalDuration.Seconds())
	fmt.Printf("測試檔案大小:      %d MB\n", ts.TransferSizeMB)
	fmt.Printf("傳輸次數:          %d\n", ts.TotalTests)
	fmt.Printf("總傳輸數據:        %.2f GB\n", float64(ts.TotalBytes)/(1024*1024*1024))
	fmt.Printf("最大傳輸效能:      %.2f Gbps\n", ts.MaxThroughput)
	fmt.Printf("最小傳輸效能:      %.2f Gbps\n", ts.MinThroughput)
	fmt.Printf("平均傳輸效能:      %.2f Gbps\n", ts.AvgThroughput)
	fmt.Println(strings.Repeat("=", 80))
	
	// 詳細統計
	fmt.Println("\n詳細傳輸記錄:")
	fmt.Println("序號\t開始時間\t\t持續時間\t發送(MB)\t接收(MB)\t發送速度\t接收速度\t整體速度")
	fmt.Println(strings.Repeat("-", 120))
	for i, stat := range ts.Stats {
		fmt.Printf("%d\t%s\t%.2fs\t\t%.2f\t\t%.2f\t\t%.2f\t\t%.2f\t\t%.2f\n", 
			i+1, 
			stat.StartTime.Format("15:04:05"), 
			stat.Duration.Seconds(),
			float64(stat.BytesSent)/(1024*1024),
			float64(stat.BytesReceived)/(1024*1024),
			stat.SendThroughput,
			stat.ReceiveThroughput,
			stat.OverallThroughput)
	}
}

// 獲取網路介面資訊
func getNetworkInterfaceInfo() (string, string) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "Unknown", "Unknown"
	}
	
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
			// 嘗試獲取介面速度（Linux specific）
			speedFile := fmt.Sprintf("/sys/class/net/%s/speed", iface.Name)
			if data, err := os.ReadFile(speedFile); err == nil {
				speed := string(data)
				return iface.Name, speed + " Mbps"
			}
			return iface.Name, "Unknown Speed"
		}
	}
	return "Unknown", "Unknown"
}

// TCP優化函數
func optimizeTCP(conn net.Conn, config *ClientConfig) error {
	if !config.Optimize {
		return nil
	}
	
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return fmt.Errorf("connection is not TCP")
	}
	
	// 設置TCP no delay
	if err := tcpConn.SetNoDelay(true); err != nil {
		log.Printf("Warning: Failed to set TCP_NODELAY: %v", err)
	}
	
	// 設置keep alive
	if err := tcpConn.SetKeepAlive(true); err != nil {
		log.Printf("Warning: Failed to set keep alive: %v", err)
	}
	
	if err := tcpConn.SetKeepAlivePeriod(30 * time.Second); err != nil {
		log.Printf("Warning: Failed to set keep alive period: %v", err)
	}
	
	// 嘗試設置socket buffer大小（Linux specific）
	if runtime.GOOS == "linux" {
		if rawConn, err := tcpConn.SyscallConn(); err == nil {
			rawConn.Control(func(fd uintptr) {
				// 設置發送緩衝區
				syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_SNDBUF, SocketBufferSize)
				// 設置接收緩衝區
				syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, SocketBufferSize)
			})
		}
	}
	
	return nil
}

// 檢查並啟用BBR
func enableBBR() error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("BBR is only supported on Linux")
	}
	
	// 檢查BBR是否可用
	if _, err := os.Stat("/proc/sys/net/ipv4/tcp_congestion_control"); err != nil {
		return fmt.Errorf("TCP congestion control not available")
	}
	
	// 嘗試設置BBR
	file, err := os.OpenFile("/proc/sys/net/ipv4/tcp_congestion_control", os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("failed to open congestion control file: %v", err)
	}
	defer file.Close()
	
	if _, err := file.WriteString("bbr"); err != nil {
		return fmt.Errorf("failed to set BBR: %v", err)
	}
	
	log.Println("BBR congestion control enabled")
	return nil
}

// 測試與目標服務器的連通性
func testConnectivity(serverHost string, port int, timeout time.Duration) error {
	log.Printf("Testing connectivity to %s:%d...", serverHost, port)
	
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", serverHost, port), timeout)
	if err != nil {
		return fmt.Errorf("failed to connect to %s:%d: %v", serverHost, port, err)
	}
	conn.Close()
	
	log.Printf("✓ Connectivity test successful to %s:%d", serverHost, port)
	return nil
}

// 驗證IP地址格式
func validateIPAddress(ip string) error {
	if ip == "localhost" {
		return nil // localhost is valid
	}
	
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		// 嘗試解析為域名
		_, err := net.LookupHost(ip)
		if err != nil {
			return fmt.Errorf("invalid IP address or hostname: %s", ip)
		}
	}
	return nil
}

// 單一連接傳輸 (Mirror模式：發送後也接收)
func performTransfer(config *ClientConfig, transferSize int64, connId int, wg *sync.WaitGroup, 
	totalSentBytes *int64, totalReceivedBytes *int64, errors *int64) {
	defer wg.Done()
	
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", config.ServerHost, config.Port))
	if err != nil {
		atomic.AddInt64(errors, 1)
		if config.Debug {
			log.Printf("Connection %d failed: %v", connId, err)
		}
		return
	}
	defer conn.Close()
	
	// 優化TCP設置
	if err := optimizeTCP(conn, config); err != nil {
		log.Printf("Warning: TCP optimization failed for connection %d: %v", connId, err)
	}
	
	// Phase 1: 發送數據到伺服器
	sendBuffer := make([]byte, config.BufferSize)
	if _, err := rand.Read(sendBuffer); err != nil {
		atomic.AddInt64(errors, 1)
		if config.Debug {
			log.Printf("Connection %d: Failed to generate random data: %v", connId, err)
		}
		return
	}
	
	var sent int64
	for sent < transferSize {
		remaining := transferSize - sent
		writeSize := int64(len(sendBuffer))
		if remaining < writeSize {
			writeSize = remaining
		}
		
		conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		n, err := conn.Write(sendBuffer[:writeSize])
		if err != nil {
			atomic.AddInt64(errors, 1)
			if config.Debug {
				log.Printf("Connection %d write error: %v", connId, err)
			}
			return
		}
		sent += int64(n)
		
		if config.Debug && sent%1048576 == 0 { // 每1MB打印一次
			log.Printf("Connection %d sent %d MB", connId, sent/1048576)
		}
	}
	
	// 發送完畢後，給服務器一些時間處理數據
	// 不立即關閉寫入端，讓服務器有時間響應
	time.Sleep(100 * time.Millisecond)
	
	// Phase 2: Mirror模式 - 接收伺服器發送回來的數據
	receiveBuffer := make([]byte, config.BufferSize)
	var received int64
	
	for received < transferSize {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second)) // 增加超時時間
		n, err := conn.Read(receiveBuffer)
		if err != nil {
			if err == io.EOF {
				break
			}
			atomic.AddInt64(errors, 1)
			if config.Debug {
				log.Printf("Connection %d read error: %v", connId, err)
			}
			return
		}
		received += int64(n)
	}
	
	atomic.AddInt64(totalSentBytes, sent)
	atomic.AddInt64(totalReceivedBytes, received)
	
	if config.Debug {
		log.Printf("Connection %d completed: sent=%d bytes, received=%d bytes", connId, sent, received)
	}
}

// 執行單次測試
func performSingleTest(config *ClientConfig, transferSize int64) TransferStats {
	start := time.Now()
	
	var wg sync.WaitGroup
	var totalSentBytes int64
	var totalReceivedBytes int64
	var errors int64
	
	// 啟動多個並行連接
	for i := 0; i < config.Connections; i++ {
		wg.Add(1)
		go performTransfer(config, transferSize/int64(config.Connections), i, &wg, &totalSentBytes, &totalReceivedBytes, &errors)
	}
	
	wg.Wait()
	end := time.Now()
	duration := end.Sub(start)
	
	// 計算各種吞吐量
	sendThroughputGbps := float64(totalSentBytes*8) / float64(duration.Nanoseconds()) * 1e9 / 1e9
	receiveThroughputGbps := float64(totalReceivedBytes*8) / float64(duration.Nanoseconds()) * 1e9 / 1e9
	overallThroughputGbps := float64((totalSentBytes+totalReceivedBytes)*8) / float64(duration.Nanoseconds()) * 1e9 / 1e9
	
	if errors > 0 {
		log.Printf("Test completed with %d errors", errors)
	}
	
	return TransferStats{
		StartTime:         start,
		EndTime:          end,
		BytesSent:        totalSentBytes,
		BytesReceived:    totalReceivedBytes,
		Duration:         duration,
		SendThroughput:   sendThroughputGbps,
		ReceiveThroughput: receiveThroughputGbps,
		OverallThroughput: overallThroughputGbps,
		ConnectionCount:  config.Connections,
	}
}

// 執行完整測試循環
func runTestLoop(config *ClientConfig, transferSize int64) {
	session := &TestSession{
		TransferSizeMB: int(transferSize / (1024 * 1024)),
	}
	
	// 驗證目標IP地址
	if err := validateIPAddress(config.ServerHost); err != nil {
		log.Fatalf("IP validation failed: %v", err)
	}
	
	// 測試連通性
	if err := testConnectivity(config.ServerHost, config.Port, 10*time.Second); err != nil {
		log.Fatalf("Connectivity test failed: %v", err)
	}
	
	// 獲取網路介面資訊
	session.NetworkInterface, session.InterfaceSpeed = getNetworkInterfaceInfo()
	
	// 如果啟用優化，嘗試啟用BBR
	if config.Optimize {
		if err := enableBBR(); err != nil {
			log.Printf("Warning: Failed to enable BBR: %v", err)
		}
	}
	
	log.Printf("Starting test loop for %d seconds", config.TestDuration)
	log.Printf("Transfer size: %dMB per test, Connections: %d", 
		session.TransferSizeMB, config.Connections)
	
	testStartTime := time.Now()
	testNumber := 1
	
	for {
		elapsed := time.Since(testStartTime)
		if elapsed.Seconds() >= float64(config.TestDuration) {
			break
		}
		
		log.Printf("\n開始第 %d 次測試 (已測試 %.1f 秒)...", testNumber, elapsed.Seconds())
		
		stat := performSingleTest(config, transferSize)
		session.AddTransferStat(stat)
		
		log.Printf("第 %d 次測試完成: 發送=%.2fMB 接收=%.2fMB 用時=%.2fs 整體速度=%.2fGbps", 
			testNumber, 
			float64(stat.BytesSent)/(1024*1024), 
			float64(stat.BytesReceived)/(1024*1024),
			stat.Duration.Seconds(), 
			stat.OverallThroughput)
		
		testNumber++
		
		// 短暫休息避免過度負載
		time.Sleep(1 * time.Second)
	}
	
	session.TotalDuration = time.Since(testStartTime)
	session.PrintSummary()
}

func main() {
	var (
		serverHost   = flag.String("c", "", "Connect to server IP address (like iperf -c)")
		hostFlag     = flag.String("host", "localhost", "Server hostname or IP (deprecated, use -c instead)")
		port         = flag.Int("port", DefaultPort, "Server port")
		connections  = flag.Int("P", DefaultConnections, "Number of parallel connections")
		bufferSize   = flag.Int("buffer", DefaultBufferSize, "Buffer size in bytes")
		transferMB   = flag.Int("size", DefaultTransferSize/(1024*1024), "Transfer size in MB per test")
		testDuration = flag.Int("t", 60, "Total test duration in seconds")
		optimize     = flag.Bool("opti", false, "Enable TCP optimizations including BBR")
		debug        = flag.Bool("debug", false, "Enable debug logging")
	)
	flag.Parse()
	
	// 處理 -c 參數，如果沒有指定則使用 -host
	targetHost := *serverHost
	if targetHost == "" {
		targetHost = *hostFlag
	}
	
	if targetHost == "" {
		log.Fatalf("Error: Must specify target server using -c <IP> (like iperf)")
	}
	
	// 驗證參數
	if *connections > MaxConnections {
		log.Fatalf("Maximum connections cannot exceed %d", MaxConnections)
	}
	
	if *testDuration <= 0 {
		log.Fatalf("Test duration must be positive")
	}
	
	config := &ClientConfig{
		ServerHost:   targetHost,
		Port:         *port,
		Connections:  *connections,
		BufferSize:   *bufferSize,
		Optimize:     *optimize,
		Debug:        *debug,
		TestDuration: *testDuration,
	}
	
	transferSize := int64(*transferMB) * 1024 * 1024
	
	log.Printf("Starting TCP client (Mirror Mode)")
	log.Printf("Target: %s:%d", config.ServerHost, config.Port)
	log.Printf("Test duration: %d seconds", config.TestDuration)
	log.Printf("Transfer size: %dMB per test, Buffer: %dMB", 
		*transferMB, *bufferSize/(1024*1024))
	log.Printf("Parallel connections: %d, Optimize: %v", *connections, *optimize)
	
	runTestLoop(config, transferSize)
}