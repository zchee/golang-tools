// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package lsprpc_test

import (
	"context"
	"log"
	"testing"

	jsonrpc2_v2 "golang.org/x/tools/internal/jsonrpc2_v2"

	"golang.org/x/tools/internal/lsp/cache"
	. "golang.org/x/tools/internal/lsp/lsprpc"
)

func TestJSONRpc2V2(t *testing.T) {
	ctx := context.Background()
	listener, err := jsonrpc2_v2.NetPipe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	}()
	c := cache.New(ctx, nil)
	b := NewServeBinder(c)
	server, err := jsonrpc2_v2.Serve(ctx, listener, b, jsonrpc2_v2.ServeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error)
	go func() {
		done <- server.Wait(context.Background())
	}()
	conn, err := jsonrpc2_v2.Dial(ctx, listener.Dialer(ctx), ClientBinder{})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		log.Fatal(err)
	}
	if err := <-done; err != nil {
		log.Fatal(err)
	}
}
