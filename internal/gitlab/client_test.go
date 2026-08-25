package gitlab

import (
	"net/http"
	"testing"
	"time"
)

func TestNewDefault(t *testing.T) {
	c, err := New("https://gitlab.com", "token", Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	if c.baseURL != "https://gitlab.com" {
		t.Fatalf("baseURL 异常: %s", c.baseURL)
	}
	if c.httpClient.Timeout != 5*time.Second {
		t.Fatalf("timeout 异常: %v", c.httpClient.Timeout)
	}
}

func TestNewInsecureSkipVerify(t *testing.T) {
	c, err := New("https://gitlab.example.com", "token", Options{
		Timeout:            5 * time.Second,
		InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatalf("创建客户端失败: %v", err)
	}
	transport := c.httpClient.Transport.(*http.Transport)
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("期望 InsecureSkipVerify=true")
	}
}

func TestNewCACertFileMissing(t *testing.T) {
	_, err := New("https://gitlab.example.com", "token", Options{
		Timeout:    5 * time.Second,
		CACertFile: "/nonexistent/ca.pem",
	})
	if err == nil {
		t.Fatal("期望读取不存在的 CA 证书文件报错")
	}
}
