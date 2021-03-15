// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc

import (
	"context"

	jsonrpc2_v2 "golang.org/x/tools/internal/jsonrpc2_v2"
	"golang.org/x/tools/internal/lsp"
	"golang.org/x/tools/internal/lsp/cache"
	"golang.org/x/tools/internal/lsp/protocol"
)

type ServeBinder struct {
	cache *cache.Cache
}

func NewServeBinder(cache *cache.Cache) *ServeBinder {
	return &ServeBinder{
		cache: cache,
	}
}

func (b *ServeBinder) Bind(conn *jsonrpc2_v2.Connection) (jsonrpc2_v2.ConnectionOptions, error) {
	client := protocol.ClientDispatcherV2(conn)
	session := b.cache.NewSession(context.Background())
	server := lsp.NewServer(session, client)
	return jsonrpc2_v2.ConnectionOptions{
		Handler: protocol.ServerHandlerV2(server),
	}, nil
}

type ForwardBinder struct {
	dialer jsonrpc2_v2.Dialer
}

func NewForwardBinder(dialer jsonrpc2_v2.Dialer) *ForwardBinder {
	return &ForwardBinder{
		dialer: dialer,
	}
}

type ClientBinder struct{}

func (ClientBinder) Bind(conn *jsonrpc2_v2.Connection) (jsonrpc2_v2.ConnectionOptions, error) {
	client := protocol.ClientDispatcherV2(conn)
	return jsonrpc2_v2.ConnectionOptions{
		Handler: protocol.ClientHandlerV2(client),
	}, nil
}

func (b *ForwardBinder) Bind(conn *jsonrpc2_v2.Connection) (opts jsonrpc2_v2.ConnectionOptions, _ error) {
	serverConn, err := jsonrpc2_v2.Dial(context.Background(), b.dialer, ClientBinder{})
	if err != nil {
		return opts, err
	}
	server := protocol.ServerDispatcherV2(serverConn)
	return jsonrpc2_v2.ConnectionOptions{
		Handler: protocol.ServerHandlerV2(server),
	}, nil
}
