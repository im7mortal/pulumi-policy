// Copyright 2016-2021, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integrationtests

import "sync"

type PriorityMutex struct {
	mu          sync.Mutex
	group2Wants bool
	cond        *sync.Cond
}

func NewPriorityMutex() *PriorityMutex {
	pm := &PriorityMutex{}
	pm.cond = sync.NewCond(&pm.mu)
	return pm
}

func (pm *PriorityMutex) LockRegular() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for pm.group2Wants {
		pm.cond.Wait()
	}
}

func (pm *PriorityMutex) UnlockRegular() {
	pm.cond.Signal() // Allow others to proceed
}

func (pm *PriorityMutex) LockPriority() {
	pm.mu.Lock()
	pm.group2Wants = true
}

func (pm *PriorityMutex) UnlockPriority() {
	pm.group2Wants = false
	pm.mu.Unlock()
	pm.cond.Broadcast()
}
