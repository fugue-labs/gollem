package appserver

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/gorilla/websocket"
)

// ServeWebSocket serves one app-server connection over WebSocket text frames.
// Each incoming text or binary message must contain one JSON-RPC object; each
// response, server request, or notification is written as one text message.
func ServeWebSocket(ctx context.Context, server *Server, conn *websocket.Conn) error {
	if conn == nil {
		return errors.New("appserver: nil websocket connection")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	conn.SetReadLimit(maxJSONLineBytes)

	inputR, inputW := io.Pipe()
	outputR, outputW := io.Pipe()
	defer func() {
		_ = conn.Close()
		_ = inputR.Close()
		_ = inputW.Close()
		_ = outputR.Close()
		_ = outputW.Close()
	}()

	readerErr := make(chan error, 1)
	writerErr := make(chan error, 1)
	serveErr := make(chan error, 1)
	go func() {
		readerErr <- copyWebSocketToJSONLines(ctx, conn, inputW)
	}()
	go func() {
		writerErr <- copyJSONLinesToWebSocket(ctx, conn, outputR)
	}()
	go func() {
		err := ServeJSONLines(ctx, server, inputR, outputW)
		if err != nil {
			_ = outputW.CloseWithError(err)
		} else {
			_ = outputW.Close()
		}
		serveErr <- err
	}()

	var firstErr error
	select {
	case err := <-serveErr:
		firstErr = err
		if err == nil {
			if err := <-writerErr; firstErr == nil {
				firstErr = err
			}
		}
	case err := <-readerErr:
		firstErr = err
		_ = inputW.Close()
		if err := <-serveErr; firstErr == nil {
			firstErr = err
		}
	case err := <-writerErr:
		firstErr = err
		_ = outputR.Close()
		if err := <-serveErr; firstErr == nil {
			firstErr = err
		}
	case <-ctx.Done():
		firstErr = ctx.Err()
	}
	_ = conn.Close()
	if firstErr != nil && !errors.Is(firstErr, io.ErrClosedPipe) {
		return firstErr
	}
	return nil
}

func copyWebSocketToJSONLines(ctx context.Context, conn *websocket.Conn, writer *io.PipeWriter) error {
	defer func() { _ = writer.Close() }()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			if isNormalWebSocketClose(err) {
				return nil
			}
			_ = writer.CloseWithError(err)
			return fmt.Errorf("read app-server websocket message: %w", err)
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		if _, err := writer.Write(data); err != nil {
			return fmt.Errorf("write app-server websocket input: %w", err)
		}
		if _, err := writer.Write([]byte{'\n'}); err != nil {
			return fmt.Errorf("write app-server websocket input newline: %w", err)
		}
	}
}

func copyJSONLinesToWebSocket(ctx context.Context, conn *websocket.Conn, reader *io.PipeReader) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), maxJSONLineBytes)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := conn.WriteMessage(websocket.TextMessage, scanner.Bytes()); err != nil {
			return fmt.Errorf("write app-server websocket message: %w", err)
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.ErrClosedPipe) {
		return fmt.Errorf("read app-server websocket output: %w", err)
	}
	return nil
}

func isNormalWebSocketClose(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		return false
	}
	switch closeErr.Code {
	case websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived:
		return true
	default:
		return false
	}
}
