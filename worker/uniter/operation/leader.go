// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package operation

import (
	"fmt"
	"time"

	"github.com/juju/errors"
	"gopkg.in/juju/charm.v4/hooks"

	"github.com/juju/juju/worker/uniter/hook"
	"github.com/juju/juju/worker/uniter/runner"
)

type acceptLeadership struct {
	Operation
}

func (al *acceptLeadership) String() string {
	return "accept leadership"
}

func (al *acceptLeadership) Prepare(state State) (*State, error) {
	if err := al.checkState(state); err != nil {
		return nil, err
	}
	return nil, ErrSkipExecute
}

func (al *acceptLeadership) Commit(state State) (*State, error) {
	if err := al.checkState(state); err != nil {
		return nil, err
	}
	if state.Leader {
		// Nothing needs to be done -- leader is only set when queueing a
		// leader-elected hook. Therefore, if leader is true, the appropriate
		// hook is either queued or already run.
		return nil, nil
	}
	newState := stateChange{
		Kind: RunHook,
		Step: Queued,
		Hook: hook.Info{Kind: "leader-elected"},
	}.apply(state)
	newState.Leader = true
	return newState, nil
}

func (al *acceptLeadership) checkState(state State) error {
	if state.Kind != Continue {
		// We'll need to queue up a hook, and we can't do that without
		// stomping on existing state.
		return ErrCannotAcceptLeadership
	}
	return nil
}

type resignLeadership struct{}

func (rl *resignLeadership) String() string {
	return "resign leadership"
}

func (rl *resignLeadership) Prepare(state State) (*State, error) {
	if !state.Leader {
		// Nothing needs to be done -- state.Leader should only be set to
		// false when committing the leader-deposed hook.
		return nil, ErrSkipExecute
	}
	return nil, nil
}

func (rl *resignLeadership) Execute(state State) (*State, error) {
	logger.Warningf("we should run a leader-deposed hook here, but we can't yet")
	// TODO(fwereade): this hits a lot of intersecting problems.
	//
	// 1) we can't yet create a sufficiently dumbed-down hook context for a
	//    leader-deposed hook to run as specced. (This is the proximate issue,
	//    and is sufficient to prevent us from implementing this op right.)
	// 2) we want to write a state-file change, so this has to be an operation
	//    (or, at least, it has to be serialized with all other operations).
	// 3) the hook execution itself *might* not need to be serialized with
	//    other operations, which is moot until we consider that:
	// 4) we want to invoke this behaviour from elsewhere (ie when we don't
	//    have an api connection available), but:
	// 5) we can't get around the serialization requirement in (2).
	//
	// So. I *think* that the right approach is to implement a no-api uniter
	// variant, that we run *instead of* the normal uniter when the API is
	// unavailable, and replace with a real uniter when appropriate; this
	// implies that we need to take care not to allow the implementations to
	// diverge, but implementing them both as "uniters" is probably the best
	// way to encourage logic-sharing and prevent that problem.
	return nil, nil
}

func (rl *resignLeadership) Commit(state State) (*State, error) {
	state.Leader = false
	return &state, nil
}
