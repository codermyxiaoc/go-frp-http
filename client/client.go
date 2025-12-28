package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"go-frp-http/common"
)

type Config struct {
	HttpPort        int               `mapstructure:"http-port" json:"httpPort"`
	HttpsPort       int               `mapstructure:"https-port" json:"httpsPort"`
	MainPort        int               `mapstructure:"main-port" json:"mainPort"`
	Secret          string            `mapstructure:"secret" json:"secret"`
	ServerIp        string            `mapstructure:"server-ip" json:"serverIp"`
	EnableLimit     bool              `mapstructure:"enable-limit" json:"enableLimit"`
	LimitBufferSize int               `mapstructure:"limit-buffer-size" json:"limitBufferSize"`
	Connections     []*common.Connect `mapstructure:"connections" json:"connections"`
}

var config Config

func init() {
	common.InitLog()

	v := viper.NewWithOptions(viper.KeyDelimiter("|"))
	v.SetConfigName("config")
	v.AddConfigPath(".")
	v.SetConfigType("yaml")

	v.SetDefault("http-port", 80)
	v.SetDefault("main-port", 12345)
	v.SetDefault("secret", "secret")
	v.SetDefault("server-ip", "127.0.0.1")
	v.SetDefault("enable-limit", false)
	v.SetDefault("limit-buffer-size", 1024)
	v.SetDefault("https-port", 443)

	if err := v.ReadInConfig(); err != nil {
		logrus.Fatalf("读取配置文件失败: %v", err)
	}
	if err := v.Unmarshal(&config); err != nil {
		logrus.Fatalf("解析配置文件失败: %v", err)
	}
}

func main() {
	if len(config.Connections) == 0 {
		logrus.Errorf("请配置内网穿透端口映射:[connections]")
		return
	}

	var wg sync.WaitGroup
	wg.Add(len(config.Connections))

	for _, connect := range config.Connections {
		if connect.Dns == "" {
			logrus.Errorf("内网穿透端口映射:[%d]的dns域名不能为空", connect.LocalPort)
			wg.Done()
			continue
		}
		go func(cfg *common.Connect) {
			defer wg.Done()
			runClientLoop(cfg)
		}(connect)
	}
	wg.Wait()
}

func runClientLoop(connect *common.Connect) {
	backoff := time.Second
	maxBackoff := 30 * time.Second
	resetBackoff := 5 * time.Second

	for {
		err := initClient(connect)
		if err != nil {
			logrus.Errorf("[%s] 连接断开或失败: %v", connect.Dns, err)
		}

		time.Sleep(backoff)
		if backoff < maxBackoff {
			backoff *= 2
		} else {
			backoff = resetBackoff
		}
	}
}

func initClient(connect *common.Connect) error {
	mainConn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", config.ServerIp, config.MainPort), 5*time.Second)
	if err != nil {
		return fmt.Errorf("连接main服务错误: %v", err)
	}

	if err := mainConn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = mainConn.Close()
		return fmt.Errorf("设置main连接超时时间错误: %v", err)
	}

	connectBytes, err := json.Marshal(connect)
	if err != nil {
		_ = mainConn.Close()
		return fmt.Errorf("序列化穿透连接配置错误: %v", err)
	}

	if _, err := mainConn.Write(append(connectBytes, common.DELIM)); err != nil {
		_ = mainConn.Close()
		return fmt.Errorf("发送穿透连接配置失败: %v", err)
	}

	reader := bufio.NewReader(mainConn)
	taskPortStr, err := reader.ReadString(common.DELIM)
	_ = mainConn.Close()

	if err != nil {
		return fmt.Errorf("读取task端口失败: %v", err)
	}

	taskPortStr = strings.TrimSpace(taskPortStr)
	switch taskPortStr {
	case common.SECRET_ERROR:
		return fmt.Errorf("密钥错误[%s]-dns: %s", connect.Secret, connect.Dns)
	case common.TASK_PORT_ERROR:
		return fmt.Errorf("task连接端口占用[%d]-dns: %s", connect.TaskPort, connect.Dns)
	case common.DNS_ERROR:
		return fmt.Errorf("local端口[%d]-dns为空", connect.TaskPort)
	}

	taskPort, err := strconv.Atoi(taskPortStr)
	if err != nil {
		return fmt.Errorf("task端口转换错误: %v", err)
	}
	connect.TaskPort = taskPort

	return startServer(connect)
}

func startServer(connect *common.Connect) error {
	masterConn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", config.ServerIp, connect.TaskPort))
	if err != nil {
		return fmt.Errorf("连接task服务失败: %v", err)
	}
	defer func() { _ = masterConn.Close() }()

	var wg sync.WaitGroup
	wg.Add(2)

	go keepAlive(masterConn, &wg)
	go listenNotify(masterConn, connect, &wg)

	logrus.Infof("转发服务成功: %s <-> %s local: %s:%d https: https://%s:%d http: http://%s:%d",
		masterConn.LocalAddr(), masterConn.RemoteAddr(),
		connect.LocalHost, connect.LocalPort,
		connect.Dns, config.HttpsPort,
		connect.Dns, config.HttpPort)

	wg.Wait()

	return fmt.Errorf("转发服务关闭: %s <-> %s local: %s:%d",
		connect.LocalHost, connect.LocalPort,
		masterConn.LocalAddr(), masterConn.RemoteAddr())
}

func keepAlive(conn net.Conn, wg *sync.WaitGroup) {
	defer wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		if _, err := conn.Write([]byte(common.PI)); err != nil {
			logrus.Errorf("发送心跳失败: %v", err)
			return
		}
	}
}

func listenNotify(conn net.Conn, connect *common.Connect, wg *sync.WaitGroup) {
	defer wg.Done()

	reader := bufio.NewReader(conn)
	for {
		data, err := reader.ReadString(common.DELIM)
		if err != nil {
			logrus.Errorf("读取控制指令失败: %v", err)
			return
		}

		switch data {
		case common.NEW_TASK:
			go taskHandler(connect)
		case common.PI:
		default:
			logrus.Warnf("收到未知指令: %s", data)
		}
	}
}

func taskHandler(connect *common.Connect) {
	taskConn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", config.ServerIp, connect.TaskPort), 10*time.Second)
	if err != nil {
		logrus.Errorf("task连接失败: %v", err)
		return
	}

	localConn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", connect.LocalHost, connect.LocalPort), 10*time.Second)
	if err != nil {
		logrus.Errorf("连接本地服务失败: %v", err)
		_ = taskConn.Close()
		return
	}

	common.Transform(taskConn, localConn, common.TransformConfig{
		DstName:              "local",
		SrcName:              "task",
		EnableLongConnection: true,
		EnableLimit:          config.EnableLimit,
		LimitBufferSize:      config.LimitBufferSize,
	})
}
