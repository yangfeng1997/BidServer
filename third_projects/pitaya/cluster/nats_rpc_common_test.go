// Copyright (c) TFG Co. All Rights Reserved.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cluster

import (
	"fmt"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/topfreegames/pitaya/v2/helpers"
)

func getServer() *Server {
	return &Server{
		ID:       "id1",
		Type:     "type1",
		Frontend: true,
	}
}

func TestNatsRPCCommonGetChannel(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "pitaya/servers/type1/sv1", getChannel("type1", "sv1"))
	assert.Equal(t, "pitaya/servers/2type1/2sv1", getChannel("2type1", "2sv1"))
}

func TestNatsRPCCommonSetupNatsConn(t *testing.T) {
	t.Parallel()
	var conn *nats.Conn
	s := helpers.GetTestNatsServer(t)
	defer func() {
		drainAndClose(conn)
		s.Shutdown()
		s.WaitForShutdown()
	}()
	conn, err := setupNatsConn(fmt.Sprintf("nats://%s", s.Addr()), nil, nil)
	assert.NoError(t, err)
	assert.NotNil(t, conn)
}

func TestNatsRPCCommonSetupNatsConnShouldError(t *testing.T) {
	t.Parallel()
	conn, err := setupNatsConn("nats://invalid:1234", nil, nil)
	assert.Error(t, err)
	assert.Nil(t, conn)
}

func TestNatsRPCCommonCloseHandler(t *testing.T) {
	t.Parallel()
	var conn *nats.Conn
	s := helpers.GetTestNatsServer(t)
	defer func() {
		drainAndClose(conn)
		s.Shutdown()
		s.WaitForShutdown()
	}()

	dieChan := make(chan bool)

	go func() {
		value, ok := <-dieChan
		assert.True(t, ok)
		assert.True(t, value)
	}()

	conn, err := setupNatsConn(fmt.Sprintf("nats://%s", s.Addr()), dieChan, nil, nats.MaxReconnects(1),
		nats.ReconnectWait(1*time.Millisecond))
	assert.NoError(t, err)
	assert.NotNil(t, conn)
}

// TestNatsRPCCommonCloseHandlerDoesNotPanicOnClosedDieChan is a regression test
// for the shutdown race where (*App).Shutdown closes the shared dieChan while an
// async NATS ClosedHandler still fires (NATS dropped with an error concurrently
// with shutdown). Sending to the closed dieChan used to panic with "send on
// closed channel", crashing the process and aborting graceful shutdown (e.g. etcd
// deregistration). The handler must recover and return instead. Without the fix
// the async panic crashes the test binary; reaching the end means it recovered.
func TestNatsRPCCommonCloseHandlerDoesNotPanicOnClosedDieChan(t *testing.T) {
	var conn *nats.Conn
	s := helpers.GetTestNatsServer(t)

	// dieChan is already closed, simulating (*App).Shutdown having run.
	dieChan := make(chan bool)
	close(dieChan)

	conn, err := setupNatsConn(fmt.Sprintf("nats://%s", s.Addr()), dieChan, nil,
		nats.MaxReconnects(1), nats.ReconnectWait(1*time.Millisecond))
	assert.NoError(t, err)
	assert.NotNil(t, conn)

	// Force the connection to close with an error, firing ClosedHandler on the
	// async dispatcher goroutine.
	s.Shutdown()
	s.WaitForShutdown()

	// If the handler panicked the process would already be dead; reaching this
	// assertion (and the connection eventually closing) proves it recovered.
	assert.Eventually(t, conn.IsClosed, 2*time.Second, 5*time.Millisecond)
}

func TestNatsRPCCommonWaitReconnections(t *testing.T) {
	var conn *nats.Conn
	ts := helpers.GetTestNatsServer(t)
	defer func() {
		drainAndClose(conn)
		ts.Shutdown()
		ts.WaitForShutdown()
	}()

	invalidAddr := "nats://invalid:4222"
	validAddr := ts.ClientURL()

	urls := fmt.Sprintf("%s,%s", invalidAddr, validAddr)

	// Setup connection with retry enabled
	appDieCh := make(chan bool)
	conn, err := setupNatsConn(
		urls,
		appDieCh,
		nil,
		nats.ReconnectWait(10*time.Millisecond),
		nats.MaxReconnects(5),
		nats.RetryOnFailedConnect(true),
	)
	assert.NoError(t, err)
	assert.NotNil(t, conn)
	assert.True(t, conn.IsConnected())
}

func TestNatsRPCCommonDoNotBlockOnConnectionFail(t *testing.T) {
	invalidAddr := "nats://invalid:4222"

	appDieCh := make(chan bool)
	done := make(chan any)

	var conn *nats.Conn
	ts := helpers.GetTestNatsServer(t)
	defer func() {
		drainAndClose(conn)
		ts.Shutdown()
		ts.WaitForShutdown()
	}()

	go func() {
		conn, err := setupNatsConn(
			invalidAddr,
			appDieCh,
			nil,
			nats.ReconnectWait(10*time.Millisecond),
			nats.MaxReconnects(2),
			nats.RetryOnFailedConnect(true),
		)
		assert.Error(t, err)
		assert.Nil(t, conn)
		close(done)
		close(appDieCh)
	}()

	select {
	case <-appDieCh:
	case <-done:
	case <-time.After(250 * time.Millisecond):
		t.Fail()
	}
}

func TestNatsRPCCommonFailWithoutAppDieChan(t *testing.T) {
	invalidAddr := "nats://invalid:4222"

	appDieCh := make(chan bool)
	done := make(chan any)

	var conn *nats.Conn
	ts := helpers.GetTestNatsServer(t)
	defer func() {
		drainAndClose(conn)
		ts.Shutdown()
		ts.WaitForShutdown()
	}()

	go func() {
		conn, err := setupNatsConn(invalidAddr, appDieCh, nil)
		assert.Error(t, err)
		assert.Nil(t, conn)
		close(done)
		close(appDieCh)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fail()
	}
}
