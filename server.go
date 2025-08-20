package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"runtime"
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

type ServerConfig struct {
	Port        int
	Connections int
	BufferSize  int
	Optimize    bool
	Debug       bool
	BindIP      string
}

type ConnectionStats struct {
	BytesTransferred int64
	Duration         time.Duration
	Throughput       float64 // Gbps
}

type ServerStats struct {
	mu                  sync.RWMutex
	TotalConnections    int64
	TotalBytes          int64
	TotalDuration       time.Duration
	MaxThroughput       float64
	MinThroughput       float64
	AvgThroughput       float64
	ActiveConnections   int64
	ConnectionStats     []ConnectionStats
	CompletedConnections int64  // 新增：已完成的连接数
}

func (s *ServerStats) AddConnectionStat(stat ConnectionStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.TotalBytes += stat.BytesTransferred
	s.TotalDuration += stat.Duration
	s.ConnectionStats = append(s.ConnectionStats, stat)
	
	// 更新最大最小值
	if s.MaxThroughput == 0 || stat.Throughput > s.MaxThroughput {
		s.MaxThroughput = stat.Throughput
	}
	if s.MinThroughput == 0 || stat.Throughput < s.MinThroughput {
		s.MinThroughput = stat.Throughput
	}
	
	// 計算平均值
	var total float64
	for _, cs := range s.ConnectionStats {
		total += cs.Throughput
	}
	s.AvgThroughput = total / float64(len(s.ConnectionStats))
}

func (s *ServerStats) GetStats() (int64, int64, float64, float64, float64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CompletedConnections, s.TotalBytes, s.MaxThroughput, s.MinThroughput, s.AvgThroughput
}

func (s *ServerStats) IncrementCompleted() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CompletedConnections++
}

func (s *ServerStats) IncrementActive() {
	atomic.AddInt64(&s.ActiveConnections, 1)
}

func (s *ServerStats) DecrementActive() {
	atomic.AddInt64(&s.ActiveConnections, -1)
}

func (s *ServerStats) GetActiveConnections() int64 {
	return atomic.LoadInt64(&s.ActiveConnections)
}

// TCP優化函數
func optimizeTCP(conn net.Conn, config *ServerConfig) error {
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

// 獲取系統網路介面資訊
func getNetworkInterfaces() ([]net.Interface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to get network interfaces: %v", err)
	}
	return interfaces, nil
}

// 檢查網路介面是否有有效的IP地址
func checkInterfaceIPs() error {
	interfaces, err := getNetworkInterfaces()
	if err != nil {
		return err
	}

	var activeInterfaces []string
	var inactiveInterfaces []string

	for _, iface := range interfaces {
		// 跳過loopback和down的介面
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			log.Printf("Warning: Failed to get addresses for interface %s: %v", iface.Name, err)
			continue
		}

		hasValidIP := false
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				if ipnet.IP.To4() != nil { // IPv4
					activeInterfaces = append(activeInterfaces, fmt.Sprintf("%s: %s", iface.Name, ipnet.IP.String()))
					hasValidIP = true
				}
			}
		}

		if !hasValidIP {
			inactiveInterfaces = append(inactiveInterfaces, iface.Name)
		}
	}

	log.Println("Network Interface Status:")
	log.Println("========================")
	
	if len(activeInterfaces) > 0 {
		log.Println("Active interfaces with valid IP addresses:")
		for _, iface := range activeInterfaces {
			log.Printf("  ✓ %s", iface)
		}
	}

	if len(inactiveInterfaces) > 0 {
		log.Println("Interfaces without valid IP addresses:")
		for _, iface := range inactiveInterfaces {
			log.Printf("  ✗ %s (no valid IP configured)", iface)
		}
		log.Println("Please configure IP addresses for the above interfaces if needed.")
	}

	if len(activeInterfaces) == 0 {
		return fmt.Errorf("no network interfaces with valid IP addresses found")
	}

	return nil
}

// 驗證綁定IP地址
func validateBindIP(bindIP string) error {
	if bindIP == "" || bindIP == "0.0.0.0" {
		return nil // 綁定所有介面
	}

	// 檢查IP格式
	ip := net.ParseIP(bindIP)
	if ip == nil {
		return fmt.Errorf("invalid IP address format: %s", bindIP)
	}

	// 檢查IP是否存在於本機介面上
	interfaces, err := getNetworkInterfaces()
	if err != nil {
		return err
	}

	for _, iface := range interfaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				if ipnet.IP.Equal(ip) {
					log.Printf("Binding to interface %s with IP %s", iface.Name, bindIP)
					return nil
				}
			}
		}
	}

	return fmt.Errorf("IP address %s not found on any local network interface", bindIP)
}

// 處理單一連接 (Mirror模式：接收後也發送)
func handleConnection(conn net.Conn, config *ServerConfig, stats *ServerStats, transferSize int64) {
	defer conn.Close()
	stats.IncrementActive()
	defer stats.DecrementActive()
	
	start := time.Now()
	clientAddr := conn.RemoteAddr().String()
	
	if config.Debug {
		log.Printf("Handling mirror connection from %s", clientAddr)
	}
	
	// 優化TCP設置
	if err := optimizeTCP(conn, config); err != nil {
		log.Printf("Warning: TCP optimization failed for %s: %v", clientAddr, err)
	}
	
	// 創建緩衝區
	buffer := make([]byte, config.BufferSize)
	var totalBytesReceived int64
	
	// Phase 1: 接收數據
	receiveStart := time.Now()
	// 设置较短的读取超时，避免无限等待
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	
	for {
		n, err := conn.Read(buffer)
		if err != nil {
			if err == io.EOF {
				if config.Debug {
					log.Printf("Client %s finished sending, received %d bytes", clientAddr, totalBytesReceived)
				}
				break
			}
			// 检查是否是超时错误
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				if config.Debug {
					log.Printf("Read timeout from %s, received %d bytes", clientAddr, totalBytesReceived)
				}
				break
			}
			if config.Debug {
				log.Printf("Read error from %s: %v", clientAddr, err)
			}
			return
		}
		
		if n == 0 {
			break // 没有更多数据
		}
		
		totalBytesReceived += int64(n)
		
		if config.Debug && totalBytesReceived%1048576 == 0 { // 每1MB打印一次
			log.Printf("Received %d MB from %s", totalBytesReceived/1048576, clientAddr)
		}
		
		// 重置读取超时
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	}
	receiveDuration := time.Since(receiveStart)
	var receiveThroughput float64
	if receiveDuration.Nanoseconds() > 0 && totalBytesReceived > 0 {
		receiveThroughput = float64(totalBytesReceived*8) / float64(receiveDuration.Nanoseconds()) * 1e9 / 1e9
	}
	
	// Phase 2: Mirror模式 - 發送相同大小的數據回去
	sendStart := time.Now()
	var totalBytesSent int64
	
	// 只有在成功接收到数据后才发送回去
	if totalBytesReceived > 0 {
		// 生成隨機數據發送回客戶端
		sendBuffer := make([]byte, config.BufferSize)
		if _, err := rand.Read(sendBuffer); err != nil {
			log.Printf("Warning: Failed to generate random data for %s: %v", clientAddr, err)
			// 即使生成随机数据失败，也继续发送零数据
			sendBuffer = make([]byte, config.BufferSize)
		}
		
		// 发送与接收到的数据量相同的数据
		targetSendSize := totalBytesReceived
		for totalBytesSent < targetSendSize {
			remaining := targetSendSize - totalBytesSent
			writeSize := int64(len(sendBuffer))
			if remaining < writeSize {
				writeSize = remaining
			}
			
			conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
			n, err := conn.Write(sendBuffer[:writeSize])
			if err != nil {
				if config.Debug {
					log.Printf("Write error to %s: %v", clientAddr, err)
				}
				break // 不要return，继续记录统计
			}
			totalBytesSent += int64(n)
		}
	}
	sendDuration := time.Since(sendStart)
	var sendThroughput float64
	if sendDuration.Nanoseconds() > 0 && totalBytesSent > 0 {
		sendThroughput = float64(totalBytesSent*8) / float64(sendDuration.Nanoseconds()) * 1e9 / 1e9
	}
	
	totalDuration := time.Since(start)
	
	// 只有在有实际数据传输时才记录统计
	if totalBytesReceived > 0 || totalBytesSent > 0 {
		// 記錄接收統計
		if totalBytesReceived > 0 {
			receiveStats := ConnectionStats{
				BytesTransferred: totalBytesReceived,
				Duration:         receiveDuration,
				Throughput:       receiveThroughput,
			}
			stats.AddConnectionStat(receiveStats)
		}
		
		// 記錄發送統計
		if totalBytesSent > 0 {
			sendStats := ConnectionStats{
				BytesTransferred: totalBytesSent,
				Duration:         sendDuration,
				Throughput:       sendThroughput,
			}
			stats.AddConnectionStat(sendStats)
		}
		
		// 標記連接完成
		stats.IncrementCompleted()
		
		if config.Debug {
			log.Printf("Mirror connection from %s completed: RX=%d bytes/%.2f Gbps, TX=%d bytes/%.2f Gbps, Total=%.2fs", 
				clientAddr, totalBytesReceived, receiveThroughput, totalBytesSent, sendThroughput, totalDuration.Seconds())
		}
	} else {
		if config.Debug {
			log.Printf("Connection from %s completed with no data transfer", clientAddr)
		}
	}
}

// 啟動伺服器
func startServer(config *ServerConfig, transferSize int64) error {
	stats := &ServerStats{}
	
	// 檢查網路介面狀態
	if err := checkInterfaceIPs(); err != nil {
		return fmt.Errorf("network interface check failed: %v", err)
	}
	
	// 驗證綁定IP
	if err := validateBindIP(config.BindIP); err != nil {
		return fmt.Errorf("bind IP validation failed: %v", err)
	}
	
	// 如果啟用優化，嘗試啟用BBR
	if config.Optimize {
		if err := enableBBR(); err != nil {
			log.Printf("Warning: Failed to enable BBR: %v", err)
		}
	}
	
	// 構建監聽地址
	listenAddr := fmt.Sprintf("%s:%d", config.BindIP, config.Port)
	if config.BindIP == "" {
		listenAddr = fmt.Sprintf(":%d", config.Port)
	}
	
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %v", listenAddr, err)
	}
	defer listener.Close()
	
	if config.BindIP == "" {
		log.Printf("Server listening on all interfaces, port %d", config.Port)
	} else {
		log.Printf("Server listening on %s:%d", config.BindIP, config.Port)
	}
	log.Printf("Configuration: Buffer=%dMB, MaxConnections=%d, Optimize=%v", 
		config.BufferSize/(1024*1024), config.Connections, config.Optimize)
	
	// 統計輸出協程
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				connections, bytes, maxThroughput, minThroughput, avgThroughput := stats.GetStats()
				active := stats.GetActiveConnections()
				log.Printf("Stats: Active=%d, Total=%d, Bytes=%dMB, Max=%.2fGbps, Min=%.2fGbps, Avg=%.2fGbps",
					active, connections, bytes/(1024*1024), maxThroughput, minThroughput, avgThroughput)
			}
		}
	}()
	
	// 使用semaphore限制並發連接數
	semaphore := make(chan struct{}, config.Connections)
	
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Accept error: %v", err)
			continue
		}
		
		// 獲取semaphore
		semaphore <- struct{}{}
		
		go func(c net.Conn) {
			defer func() { <-semaphore }() // 釋放semaphore
			handleConnection(c, config, stats, transferSize)
		}(conn)
	}
}

func main() {
	var (
		port        = flag.Int("port", DefaultPort, "Server port")
		bindIP      = flag.String("bind", "", "IP address to bind to (empty for all interfaces)")
		connections = flag.Int("c", DefaultConnections, "Maximum concurrent connections")
		bufferSize  = flag.Int("buffer", DefaultBufferSize, "Buffer size in bytes")
		transferMB  = flag.Int("size", DefaultTransferSize/(1024*1024), "Transfer size in MB")
		optimize    = flag.Bool("opti", false, "Enable TCP optimizations including BBR")
		debug       = flag.Bool("debug", false, "Enable debug logging")
	)
	flag.Parse()
	
	// 驗證參數
	if *connections > MaxConnections {
		log.Fatalf("Maximum connections cannot exceed %d", MaxConnections)
	}
	
	config := &ServerConfig{
		Port:        *port,
		BindIP:      *bindIP,
		Connections: *connections,
		BufferSize:  *bufferSize,
		Optimize:    *optimize,
		Debug:       *debug,
	}
	
	transferSize := int64(*transferMB) * 1024 * 1024
	
	log.Printf("Starting TCP server (Mirror Mode)")
	log.Printf("Transfer size: %dMB, Buffer: %dMB", *transferMB, *bufferSize/(1024*1024))
	
	if err := startServer(config, transferSize); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}