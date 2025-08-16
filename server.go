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
}

func (s *ServerStats) AddConnectionStat(stat ConnectionStats) {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	s.TotalConnections++
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
	return s.TotalConnections, s.TotalBytes, s.MaxThroughput, s.MinThroughput, s.AvgThroughput
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
	for totalBytesReceived < transferSize {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		n, err := conn.Read(buffer)
		if err != nil {
			if err == io.EOF {
				if config.Debug {
					log.Printf("Client %s finished sending, received %d bytes", clientAddr, totalBytesReceived)
				}
				break
			}
			if config.Debug {
				log.Printf("Read error from %s: %v", clientAddr, err)
			}
			return
		}
		totalBytesReceived += int64(n)
		
		// 如果已接收到預期數據量，跳出循環
		if totalBytesReceived >= transferSize {
			break
		}
	}
	receiveDuration := time.Since(receiveStart)
	receiveThroughput := float64(totalBytesReceived*8) / float64(receiveDuration.Nanoseconds()) * 1e9 / 1e9
	
	// Phase 2: Mirror模式 - 發送相同大小的數據回去
	sendStart := time.Now()
	var totalBytesSent int64
	
	// 生成隨機數據發送回客戶端
	sendBuffer := make([]byte, config.BufferSize)
	if _, err := rand.Read(sendBuffer); err != nil {
		log.Printf("Warning: Failed to generate random data for %s: %v", clientAddr, err)
		return
	}
	
	for totalBytesSent < transferSize {
		remaining := transferSize - totalBytesSent
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
			return
		}
		totalBytesSent += int64(n)
	}
	sendDuration := time.Since(sendStart)
	sendThroughput := float64(totalBytesSent*8) / float64(sendDuration.Nanoseconds()) * 1e9 / 1e9
	
	totalDuration := time.Since(start)
	
	// 記錄接收統計
	receiveStats := ConnectionStats{
		BytesTransferred: totalBytesReceived,
		Duration:         receiveDuration,
		Throughput:       receiveThroughput,
	}
	stats.AddConnectionStat(receiveStats)
	
	// 記錄發送統計
	sendStats := ConnectionStats{
		BytesTransferred: totalBytesSent,
		Duration:         sendDuration,
		Throughput:       sendThroughput,
	}
	stats.AddConnectionStat(sendStats)
	
	if config.Debug {
		log.Printf("Mirror connection from %s completed: RX=%d bytes/%.2f Gbps, TX=%d bytes/%.2f Gbps, Total=%.2fs", 
			clientAddr, totalBytesReceived, receiveThroughput, totalBytesSent, sendThroughput, totalDuration.Seconds())
	}
}

// 啟動伺服器
func startServer(config *ServerConfig, transferSize int64) error {
	stats := &ServerStats{}
	
	// 如果啟用優化，嘗試啟用BBR
	if config.Optimize {
		if err := enableBBR(); err != nil {
			log.Printf("Warning: Failed to enable BBR: %v", err)
		}
	}
	
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", config.Port))
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %v", config.Port, err)
	}
	defer listener.Close()
	
	log.Printf("Server listening on port %d", config.Port)
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