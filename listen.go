package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"syscall"
)

// defaultPortAttempts 是默认端口被占用时总共尝试的端口数（含最初请求的端口）：
// `:8080` 被占用时会依次试到 `:8089`。
const defaultPortAttempts = 10

// listenWithFallback 监听 addr，并在允许时对「端口被占用」自动递增端口重试。
//
// allowIncrement 只在用户没有显式指定端口时为真（默认 :8080）：显式写 --addr 或
// 在配置里写 addr 都表示「我就要这个端口」，此时保持快速失败。
// 非占用类错误（端口非法、无权限等）不递增，直接返回原因。
func listenWithFallback(addr string, allowIncrement bool, logger *slog.Logger) (net.Listener, error) {
	return listenWithFallbackN(addr, allowIncrement, defaultPortAttempts, logger)
}

// listenWithFallbackN 与 listenWithFallback 相同，但可指定尝试的端口总数（便于测试）。
func listenWithFallbackN(addr string, allowIncrement bool, attempts int, logger *slog.Logger) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err == nil {
		return listener, nil
	}
	if !allowIncrement || !isAddressInUse(err) {
		return nil, err
	}

	originalErr := err
	lastAddr := addr
	for step := 1; step < attempts; step++ {
		candidate, ok := nextPort(addr, step)
		if !ok {
			break
		}
		lastAddr = candidate

		listener, attemptErr := net.Listen("tcp", candidate)
		if attemptErr == nil {
			logger.Info("端口被占用，已自动改用其它端口",
				"requested", addr, "actual", candidate)
			return listener, nil
		}
		if !isAddressInUse(attemptErr) {
			return nil, attemptErr
		}
		err = attemptErr
	}

	return nil, fmt.Errorf("从 %s 起的 %d 个端口都被占用（最后尝试 %s）：%w",
		addr, attempts, lastAddr, originalErr)
}

// nextPort 返回端口号加 step 后的地址（主机部分不变）。
// 端口非数字、为 0（内核分配）或超出范围时返回 false，表示这个地址无法递增。
func nextPort(addr string, step int) (string, bool) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "", false
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port <= 0 || port+step > 65535 {
		return "", false
	}
	return net.JoinHostPort(host, strconv.Itoa(port+step)), true
}

// isAddressInUse 判断错误是否为「地址已被占用」；其它错误不该被端口回退掩盖。
func isAddressInUse(err error) bool {
	return errors.Is(err, syscall.EADDRINUSE)
}
