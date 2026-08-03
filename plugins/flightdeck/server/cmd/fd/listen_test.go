package main

import (
	"fmt"
	"net"
	"net/url"
)

// listenOn 은 이미 쓰던 주소로 다시 듣는다. 재연결 경로 시험이 **같은 URL** 을 요구하기 때문이다
// (클라이언트가 FD_URL 을 캐시 키로도 쓰므로 주소가 바뀌면 다른 시험이 된다).
func listenOn(rawURL string) (net.Listener, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("주소 해석 실패(%s): %w", rawURL, err)
	}
	return net.Listen("tcp", u.Host)
}
