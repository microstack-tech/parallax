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

package native

import (
	"encoding/json"
	"math/big"
	"time"

	"github.com/ParallaxProtocol/parallax/v2/node/fullnode/tracers"
	"github.com/ParallaxProtocol/parallax/v2/script"
	"github.com/ParallaxProtocol/parallax/v2/util"
)

func init() {
	register("noopTracer", newNoopTracer)
}

// noopTracer is a go implementation of the Tracer interface which
// performs no action. It's mostly useful for testing purposes.
type noopTracer struct{}

// newNoopTracer returns a new noop tracer.
func newNoopTracer(ctx *tracers.Context) tracers.Tracer {
	return &noopTracer{}
}

// CaptureStart implements the PVMLogger interface to initialize the tracing operation.
func (t *noopTracer) CaptureStart(env *script.PVM, from util.Address, to util.Address, create bool, input []byte, gas uint64, value *big.Int) {
}

// CaptureEnd is called after the call finishes to finalize the tracing.
func (t *noopTracer) CaptureEnd(output []byte, gasUsed uint64, _ time.Duration, err error) {
}

// CaptureState implements the PVMLogger interface to trace a single step of VM execution.
func (t *noopTracer) CaptureState(pc uint64, op script.OpCode, gas, cost uint64, scope *script.ScopeContext, rData []byte, depth int, err error) {
}

// CaptureFault implements the PVMLogger interface to trace an execution fault.
func (t *noopTracer) CaptureFault(pc uint64, op script.OpCode, gas, cost uint64, _ *script.ScopeContext, depth int, err error) {
}

// CaptureEnter is called when PVM enters a new scope (via call, create or selfdestruct).
func (t *noopTracer) CaptureEnter(typ script.OpCode, from util.Address, to util.Address, input []byte, gas uint64, value *big.Int) {
}

// CaptureExit is called when PVM exits a scope, even if the scope didn't
// execute any code.
func (t *noopTracer) CaptureExit(output []byte, gasUsed uint64, err error) {
}

func (*noopTracer) CaptureTxStart(gasLimit uint64) {}

func (*noopTracer) CaptureTxEnd(restGas uint64) {}

// GetResult returns an empty json object.
func (t *noopTracer) GetResult() (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// Stop terminates execution of the tracer at the first opportune moment.
func (t *noopTracer) Stop(err error) {
}
