package client

import (
	"fmt"
	"log"
	"net"
	"net/http"
)

func StartRawBridge(localPort int, tsHTTP *http.Client, endpoint string) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		return err
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			continue
		}
		go func(c net.Conn) {
			defer c.Close()
			req, err := http.NewRequest(http.MethodPost, endpoint, c)
			if err != nil {
				log.Printf("[RAW] 创建请求失败: %v", err)
				return
			}
			req.Header.Set("Content-Type", "application/octet-stream")
			resp, err := tsHTTP.Do(req)
			if err != nil {
				log.Printf("[RAW] 转发失败: %v", err)
				return
			}
			resp.Body.Close()
		}(conn)
	}
}
