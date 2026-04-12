// Copyright 2025-2026 The Parallax Protocol Authors
// This file is part of the parallax library.
//
// The parallax library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The parallax library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the parallax library. If not, see <http://www.gnu.org/licenses/>.

// Package testlog provides a log handler for unit tests.
package testlog

import (
	"sync"
	"testing"

	"github.com/ParallaxProtocol/parallax/logging"
)

// Handler returns a log handler which logs to the unit test log of t.
func Handler(t *testing.T, level logging.Lvl) logging.Handler {
	return logging.LvlFilterHandler(level, &handler{t, logging.TerminalFormat(false)})
}

type handler struct {
	t   *testing.T
	fmt logging.Format
}

func (h *handler) Log(r *logging.Record) error {
	h.t.Logf("%s", h.fmt.Format(r))
	return nil
}

// logger implements log.Logger such that all output goes to the unit test log via
// t.Logf(). All methods in between logger.Trace, logger.Debug, etc. are marked as test
// helpers, so the file and line number in unit test output correspond to the call site
// which emitted the log message.
type logger struct {
	t  *testing.T
	l  logging.Logger
	mu *sync.Mutex
	h  *bufHandler
}

type bufHandler struct {
	buf []*logging.Record
	fmt logging.Format
}

func (h *bufHandler) Log(r *logging.Record) error {
	h.buf = append(h.buf, r)
	return nil
}

// Logger returns a logger which logs to the unit test log of t.
func Logger(t *testing.T, level logging.Lvl) logging.Logger {
	l := &logger{
		t:  t,
		l:  logging.New(),
		mu: new(sync.Mutex),
		h:  &bufHandler{fmt: logging.TerminalFormat(false)},
	}
	l.l.SetHandler(logging.LvlFilterHandler(level, l.h))
	return l
}

func (l *logger) Trace(msg string, ctx ...any) {
	l.t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.l.Trace(msg, ctx...)
	l.flush()
}

func (l *logger) Debug(msg string, ctx ...any) {
	l.t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.l.Debug(msg, ctx...)
	l.flush()
}

func (l *logger) Info(msg string, ctx ...any) {
	l.t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.l.Info(msg, ctx...)
	l.flush()
}

func (l *logger) Warn(msg string, ctx ...any) {
	l.t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.l.Warn(msg, ctx...)
	l.flush()
}

func (l *logger) Error(msg string, ctx ...any) {
	l.t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.l.Error(msg, ctx...)
	l.flush()
}

func (l *logger) Crit(msg string, ctx ...any) {
	l.t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.l.Crit(msg, ctx...)
	l.flush()
}

func (l *logger) New(ctx ...any) logging.Logger {
	return &logger{l.t, l.l.New(ctx...), l.mu, l.h}
}

func (l *logger) GetHandler() logging.Handler {
	return l.l.GetHandler()
}

func (l *logger) SetHandler(h logging.Handler) {
	l.l.SetHandler(h)
}

// flush writes all buffered messages and clears the buffer.
func (l *logger) flush() {
	l.t.Helper()
	for _, r := range l.h.buf {
		l.t.Logf("%s", l.h.fmt.Format(r))
	}
	l.h.buf = nil
}
