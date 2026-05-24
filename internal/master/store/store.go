package store

import (
	"sync"
	"time"

	"github.com/multi-ops/internal/protocol"
)

type Store struct {
	machines map[string]*protocol.AgentInfo
	mu       sync.RWMutex
}

func New() *Store {
	return &Store{
		machines: make(map[string]*protocol.AgentInfo),
	}
}

func (s *Store) UpdateMachine(info protocol.AgentInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	info.LastSeen = time.Now().Unix()
	info.Status = "online"

	if existing, ok := s.machines[info.AgentID]; ok {
		// Merge: only update non-zero fields from the new info
		if info.Hostname != "" {
			existing.Hostname = info.Hostname
		}
		if info.OS != "" {
			existing.OS = info.OS
		}
		if info.DistributorID != "" {
			existing.DistributorID = info.DistributorID
		}
		if info.Description != "" {
			existing.Description = info.Description
		}
		if info.Release != "" {
			existing.Release = info.Release
		}
		if info.Codename != "" {
			existing.Codename = info.Codename
		}
		if info.PublicIP != "" {
			existing.PublicIP = info.PublicIP
		}
		if info.Location != "" {
			existing.Location = info.Location
		}
		if info.CPUModel != "" {
			existing.CPUModel = info.CPUModel
		}
		if info.CPUCores > 0 {
			existing.CPUCores = info.CPUCores
		}
		if info.MemoryMB > 0 {
			existing.MemoryMB = info.MemoryMB
		}
		if info.DiskGB > 0 {
			existing.DiskGB = info.DiskGB
		}
		if info.DiskUsed > 0 {
			existing.DiskUsed = info.DiskUsed
		}
		if info.Uptime > 0 {
			existing.Uptime = info.Uptime
		}
		existing.LastSeen = info.LastSeen
		existing.Status = info.Status
	} else {
		s.machines[info.AgentID] = &info
	}
}

func (s *Store) SetOffline(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.machines[agentID]; ok {
		m.Status = "offline"
	}
}

func (s *Store) GetMachine(id string) (*protocol.AgentInfo, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	m, ok := s.machines[id]
	if !ok {
		return nil, false
	}
	cp := *m
	return &cp, true
}

func (s *Store) GetAllMachines() []protocol.AgentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]protocol.AgentInfo, 0, len(s.machines))
	for _, m := range s.machines {
		result = append(result, *m)
	}
	return result
}

func (s *Store) GetOnlineMachines() []protocol.AgentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]protocol.AgentInfo, 0)
	for _, m := range s.machines {
		if m.Status == "online" {
			result = append(result, *m)
		}
	}
	return result
}

func (s *Store) CleanupStale(threshold time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := time.Now().Add(-threshold).Unix()
	for _, m := range s.machines {
		if m.LastSeen < cutoff && m.Status == "online" {
			m.Status = "offline"
		}
	}
}

// SetTags sets tags for a machine
func (s *Store) SetTags(agentID string, tags []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.machines[agentID]; ok {
		m.Tags = tags
	}
}

// SetGroup sets the group for a machine
func (s *Store) SetGroup(agentID, group string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.machines[agentID]; ok {
		m.Group = group
	}
}

// GetMachinesByTag returns all machines with the given tag
func (s *Store) GetMachinesByTag(tag string) []protocol.AgentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]protocol.AgentInfo, 0)
	for _, m := range s.machines {
		for _, t := range m.Tags {
			if t == tag {
				result = append(result, *m)
				break
			}
		}
	}
	return result
}

// GetMachinesByGroup returns all machines in the given group
func (s *Store) GetMachinesByGroup(group string) []protocol.AgentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]protocol.AgentInfo, 0)
	for _, m := range s.machines {
		if m.Group == group {
			result = append(result, *m)
		}
	}
	return result
}

// GetAllTags returns all unique tags
func (s *Store) GetAllTags() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool)
	for _, m := range s.machines {
		for _, t := range m.Tags {
			seen[t] = true
		}
	}
	tags := make([]string, 0, len(seen))
	for t := range seen {
		tags = append(tags, t)
	}
	return tags
}

// GetAllGroups returns all unique groups
func (s *Store) GetAllGroups() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]bool)
	for _, m := range s.machines {
		if m.Group != "" {
			seen[m.Group] = true
		}
	}
	groups := make([]string, 0, len(seen))
	for g := range seen {
		groups = append(groups, g)
	}
	return groups
}

// UpdateMetrics updates the real-time metrics for a machine
func (s *Store) UpdateMetrics(agentID string, metrics *protocol.MachineMetrics) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m, ok := s.machines[agentID]; ok {
		m.Metrics = metrics
		m.LastSeen = time.Now().Unix()
		m.Status = "online"
	}
}
