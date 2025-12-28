package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"go-frp-http/common"
	"golang.org/x/time/rate"
)

type Certificate struct {
	CertPath string `mapstructure:"cert-path" json:"certPath"`
	KeyPath  string `mapstructure:"key-path" json:"keyPath"`
}

type Config struct {
	HttpPort        int                    `mapstructure:"http-port" json:"httpPort"`
	MainPort        int                    `mapstructure:"main-port" json:"mainPort"`
	ConnChanCount   int                    `mapstructure:"conn-chan-count" json:"connChanCount"`
	Secret          string                 `mapstructure:"secret" json:"secret"`
	MaxIdleConns    int                    `mapstructure:"max-idle-conns" json:"maxIdleConns"`
	IdleConnTimeout int                    `mapstructure:"idle-conn-timeout" json:"idleConnTimeout"`
	KeepAliveTime   int                    `mapstructure:"keep-alive-time" json:"keepAliveTime"`
	EnableTls       bool                   `mapstructure:"enable-tls" json:"enableTls"`
	HttpsPort       int                    `mapstructure:"https-port" json:"httpsPort"`
	Certificates    map[string]Certificate `mapstructure:"certificates" json:"certificates"`
	EnableLimit     bool                   `mapstructure:"enable-limit" json:"enableLimit"`
	LimitBufferSize int                    `mapstructure:"limit-buffer-size" json:"limitBufferSize"`
}

type Task struct {
	taskConnChan <-chan net.Conn
	notifyChan   chan<- struct{}
}

var (
	certificateMap sync.Map
	config         Config
	taskMap        sync.Map
	bufferPool     = &sync.Pool{
		New: func() interface{} {
			return make([]byte, 32*1024)
		},
	}
)

func init() {
	common.InitLog()

	v := viper.NewWithOptions(viper.KeyDelimiter("|"))
	v.SetConfigName("config")
	v.AddConfigPath(".")
	v.SetConfigType("yaml")

	v.SetDefault("http-port", 80)
	v.SetDefault("main-port", 12345)
	v.SetDefault("conn-chan-count", 200)
	v.SetDefault("max-idle-conns", 100)
	v.SetDefault("idle-conn-timeout", 90)
	v.SetDefault("keep-alive-time", 10)
	v.SetDefault("enable-tls", false)
	v.SetDefault("https-port", 443)
	v.SetDefault("enable-limit", false)
	v.SetDefault("limit-buffer-size", 1024)
	v.SetDefault("secret", "secret")

	if err := v.ReadInConfig(); err != nil {
		logrus.Fatalf("读取配置文件失败: %v", err)
	}
	if err := v.Unmarshal(&config); err != nil {
		logrus.Fatalf("解析配置文件失败: %v", err)
	}
}

func main() {
	var mainWG sync.WaitGroup
	if config.EnableTls {
		mainWG.Add(3)
		go startHttpsServer(config.HttpsPort, &mainWG)
	} else {
		mainWG.Add(2)
	}
	go startMainServer(config.MainPort, &mainWG)
	go startHttpServer(config.HttpPort, &mainWG)
	mainWG.Wait()
}

type proxyBufferPool struct{}

func (p *proxyBufferPool) Get() []byte {
	return bufferPool.Get().([]byte)
}

func (p *proxyBufferPool) Put(b []byte) {
	bufferPool.Put(b)
}

func NewReverseProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			if req.URL.Host == "" {
				req.URL.Host = req.Host
			}
		},
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host := addr
				if idx := strings.Index(addr, ":"); idx != -1 {
					host = addr[:idx]
				}

				value, ok := taskMap.Load(host)
				if !ok {
					return nil, fmt.Errorf("本地服务器未启动: %s", host)
				}
				task, ok := value.(Task)
				if !ok {
					return nil, fmt.Errorf("本地服务器未启动: %s", host)
				}

				select {
				case task.notifyChan <- struct{}{}:
				default:
					return nil, fmt.Errorf("通知任务通道已满: %s", host)
				}

				select {
				case conn := <-task.taskConnChan:
					if conn == nil {
						return nil, fmt.Errorf("任务连接通道关闭: %s", host)
					}
					if config.EnableLimit {
						conn = common.NewRateLimitedConn(conn, rate.NewLimiter(rate.Limit(config.LimitBufferSize*1024), config.LimitBufferSize*1024))
					}
					return conn, nil
				case <-ctx.Done():
					go drainTaskConn(task)
					return nil, ctx.Err()
				case <-time.After(5 * time.Second):
					go drainTaskConn(task)
					return nil, fmt.Errorf("获取任务连接超时: %s", host)
				}
			},
			MaxIdleConns:      config.MaxIdleConns,
			IdleConnTimeout:   time.Duration(config.IdleConnTimeout) * time.Second,
			DisableKeepAlives: true,
		},
		BufferPool: &proxyBufferPool{},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			logrus.Errorf("代理错误 [%s] -> [%s]: %v", r.RemoteAddr, r.Host, err)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte("Bad Gateway"))
		},
	}
}

func startHttpsServer(port int, mainWG *sync.WaitGroup) {
	defer mainWG.Done()
	proxy := NewReverseProxy()
	server := http.Server{
		Addr: fmt.Sprintf(":%d", port),
		TLSConfig: &tls.Config{
			SessionTicketsDisabled: true,
			GetCertificate: func(info *tls.ClientHelloInfo) (*tls.Certificate, error) {
				certValue, ok := certificateMap.Load(info.ServerName)
				if !ok {
					return nil, fmt.Errorf("未找到证书: %s", info.ServerName)
				}
				certificate, ok := certValue.(Certificate)
				if !ok {
					return nil, fmt.Errorf("未找到证书: %s", info.ServerName)
				}
				cert, err := tls.LoadX509KeyPair(certificate.CertPath, certificate.KeyPath)
				if err != nil {
					return nil, fmt.Errorf("加载证书错误: %v", err)
				}
				return &cert, nil
			},
			MaxVersion: tls.VersionTLS12,
		},
		Handler: proxy,
	}
	if err := server.ListenAndServeTLS("", ""); err != nil {
		logrus.Errorf("启动https服务错误: %v", err)
	}
}

func startHttpServer(port int, mainWG *sync.WaitGroup) {
	defer mainWG.Done()
	proxy := NewReverseProxy()
	if err := http.ListenAndServe(fmt.Sprintf(":%d", port), proxy); err != nil {
		logrus.Errorf("启动http服务错误: %v", err)
	}
}

func drainTaskConn(task Task) {
	select {
	case conn := <-task.taskConnChan:
		if conn != nil {
			_ = conn.Close()
		}
	case <-time.After(10 * time.Second):
	}
}

func startMainServer(mainPort int, mainWG *sync.WaitGroup) {
	defer mainWG.Done()

	mainListen, err := net.Listen("tcp", fmt.Sprintf(":%d", mainPort))
	if err != nil {
		logrus.Errorf("启动main服务器错误: %v", err)
		return
	}
	logrus.Infof("main server start success port: %s", mainListen.Addr())

	for {
		mainConn, err := mainListen.Accept()
		if err != nil {
			logrus.Errorf("main服务连接失败: %v", err)
			continue
		}
		go initServer(mainConn)
	}
}

func initServer(mainConn net.Conn) {
	defer func() { _ = mainConn.Close() }()

	if err := mainConn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		logrus.Errorf("设置main连接读超时错误: %v", err)
		return
	}

	reader := bufio.NewReader(mainConn)
	connectBytes, err := reader.ReadBytes(common.DELIM)
	if err != nil {
		logrus.Errorf("读取穿透连接配置错误: %v", err)
		return
	}

	if err := mainConn.SetDeadline(time.Time{}); err != nil {
		logrus.Errorf("重置main连接读超时错误: %v", err)
		return
	}

	var connect common.Connect
	if err := json.Unmarshal(connectBytes, &connect); err != nil {
		logrus.Errorf("反序列化穿透连接配置失败: %v", err)
		return
	}

	if len(connect.Secret) != len(config.Secret) || config.Secret != connect.Secret {
		logrus.Errorf("密钥错误[%s]-dns: %s", connect.Secret, connect.Dns)
		_, _ = mainConn.Write(append([]byte(common.SECRET_ERROR), common.DELIM))
		return
	}

	if connect.Dns == "" {
		logrus.Errorf("dns域名不能为空")
		_, _ = mainConn.Write(append([]byte(common.DNS_ERROR), common.DELIM))
		return
	}

	taskListen, err := net.Listen("tcp", fmt.Sprintf(":%d", connect.TaskPort))
	if err != nil {
		logrus.Errorf("启动task服务失败: %v", err)
		if common.IsPortInUse(err) {
			_, _ = mainConn.Write(append([]byte(common.TASK_PORT_ERROR), common.DELIM))
		}
		return
	}

	connect.TaskPort = taskListen.Addr().(*net.TCPAddr).Port
	if _, err := mainConn.Write([]byte(fmt.Sprintf("%d%c", connect.TaskPort, common.DELIM))); err != nil {
		logrus.Errorf("发送task端口失败: %v", err)
		_ = taskListen.Close()
		return
	}

	go startServer(taskListen, &connect)
}

func startServer(taskListen net.Listener, connect *common.Connect) {
	_ = taskListen.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
	masterConn, err := taskListen.Accept()
	if err != nil {
		logrus.Errorf("获取任务服务master连接错误: %v", err)
		_ = taskListen.Close()
		return
	}
	_ = taskListen.(*net.TCPListener).SetDeadline(time.Time{})

	var wg sync.WaitGroup
	wg.Add(2)

	notifyChan := make(chan struct{}, config.ConnChanCount)
	taskConnChan := make(chan net.Conn, config.ConnChanCount)
	exitSignal := make(chan struct{})

	taskMap.Store(connect.Dns, Task{
		taskConnChan: taskConnChan,
		notifyChan:   notifyChan,
	})

	if config.EnableTls && connect.Type == "https" {
		if certificate, ok := config.Certificates[connect.Dns]; ok {
			certificateMap.Store(connect.Dns, certificate)
			logrus.Infof("查找到dns:[%s]的证书，加载到证书列表", connect.Dns)
		} else {
			logrus.Warningf("未找到dns:[%s]的证书", connect.Dns)
		}
	}

	go listenNotify(masterConn, notifyChan, exitSignal, &wg)
	go listenTaskConn(taskListen, taskConnChan, notifyChan, &wg)

	logrus.Infof("转发服务就绪: %s <-> %s dns: %s", masterConn.LocalAddr(), masterConn.RemoteAddr(), connect.Dns)

	<-exitSignal
	logrus.Infof("转发服务关闭: %s <-> %s dns: %s", masterConn.LocalAddr(), masterConn.RemoteAddr(), connect.Dns)
	_ = taskListen.Close()
	_ = masterConn.Close()
	wg.Wait()
	taskMap.Delete(connect.Dns)
	certificateMap.Delete(connect.Dns)
	logrus.Infof("卸载证书: %s", connect.Dns)
}

func listenNotify(masterConn net.Conn, notifyChan <-chan struct{}, exitSignal chan<- struct{}, wg *sync.WaitGroup) {
	defer func() {
		wg.Done()
		select {
		case exitSignal <- struct{}{}:
		default:
		}
	}()

	ticker := time.NewTicker(time.Duration(config.KeepAliveTime) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-notifyChan:
			if _, err := masterConn.Write([]byte(common.NEW_TASK)); err != nil {
				logrus.Errorf("发送new指令失败: %v", err)
				return
			}
		case <-ticker.C:
			if _, err := masterConn.Write([]byte(common.PI)); err != nil {
				logrus.Errorf("发送心跳包失败: %v", err)
				return
			}
		}
	}
}

func listenTaskConn(taskListen net.Listener, taskConnChan chan<- net.Conn, notifyChan chan<- struct{}, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		taskConn, err := taskListen.Accept()
		if err != nil {
			if common.IsAcceptError(err) {
				return
			}
			logrus.Errorf("获取任务连接失败: %v", err)
			return
		}

		select {
		case taskConnChan <- taskConn:
		case <-time.After(5 * time.Second):
			logrus.Warningf("任务连接入队超时，丢弃连接")
			select {
			case notifyChan <- struct{}{}:
			default:
			}
			_ = taskConn.Close()
		}
	}
}
