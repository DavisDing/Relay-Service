package monitor

import (
	"maps"
	"slices"
)

func networkClonePtr[T any](value *T) *T {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func networkCopyNodes(nodes []NetworkNode) []NetworkNode {
	out := slices.Clone(nodes)
	for i := range out {
		n := &out[i]
		n.DelayMS = networkClonePtr(n.DelayMS)
		n.CheckedAt = networkClonePtr(n.CheckedAt)
		n.Subscriptions = slices.Clone(n.Subscriptions)
		n.Availability24H = networkClonePtr(n.Availability24H)
		if n.Availability24H != nil {
			n.Availability24H.SuccessRate = networkClonePtr(n.Availability24H.SuccessRate)
			n.Availability24H.Intervals = slices.Clone(n.Availability24H.Intervals)
		}
	}
	return out
}

func networkCopySubscriptions(subs []NetworkSubscription) []NetworkSubscription {
	out := slices.Clone(subs)
	for i := range out {
		s := &out[i]
		s.UploadBytes = networkClonePtr(s.UploadBytes)
		s.DownloadBytes = networkClonePtr(s.DownloadBytes)
		s.UsedBytes = networkClonePtr(s.UsedBytes)
		s.RemainingBytes = networkClonePtr(s.RemainingBytes)
		s.TotalBytes = networkClonePtr(s.TotalBytes)
		s.ExpireSeconds = networkClonePtr(s.ExpireSeconds)
		s.UpdatedAt = networkClonePtr(s.UpdatedAt)
		s.Nodes = networkCopyNodes(s.Nodes)
	}
	return out
}

// Detach every mutable field under the worker lock. Strings/scalars can share
// their immutable values; serialization belongs outside the critical section.
func networkCopyView(v NetworkView) NetworkView {
	v.CollectedAt = networkClonePtr(v.CollectedAt)
	v.Settings.ImportedSubscriptions = networkCopySubscriptions(v.Settings.ImportedSubscriptions)
	v.Subscriptions = networkCopySubscriptions(v.Subscriptions)
	v.Nodes = networkCopyNodes(v.Nodes)
	v.Connections = slices.Clone(v.Connections)
	for i := range v.Connections {
		v.Connections[i].Chains = slices.Clone(v.Connections[i].Chains)
		v.Connections[i].StartedAt = networkClonePtr(v.Connections[i].StartedAt)
	}
	v.Logs = slices.Clone(v.Logs)
	v.Usage = slices.Clone(v.Usage)
	v.UsageGroups = slices.Clone(v.UsageGroups)
	v.UsageGroupStart = networkClonePtr(v.UsageGroupStart)
	v.UsageGroupEnd = networkClonePtr(v.UsageGroupEnd)
	v.NodeHistory = slices.Clone(v.NodeHistory)
	for i := range v.NodeHistory {
		v.NodeHistory[i].DelayMS = networkClonePtr(v.NodeHistory[i].DelayMS)
		v.NodeHistory[i].CheckedAt = networkClonePtr(v.NodeHistory[i].CheckedAt)
	}
	v.Coverage.SubscriptionSources = networkClonePtr(v.Coverage.SubscriptionSources)
	v.Coverage.StartedAt = networkClonePtr(v.Coverage.StartedAt)
	v.Coverage.LastSampleAt = networkClonePtr(v.Coverage.LastSampleAt)
	v.Coverage.SourceStates = maps.Clone(v.Coverage.SourceStates)
	v.Coverage.Notes = slices.Clone(v.Coverage.Notes)
	return v
}
