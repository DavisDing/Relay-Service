package monitor

import "fmt"

func (w *networkWorker) updateSubscriptionSources() {
	evidence := w.view.Coverage.SubscriptionSources
	if evidence == nil {
		evidence = &NetworkSubscriptionSources{CoreState: "waiting"}
		w.view.Coverage.SubscriptionSources = evidence
	}
	evidence.ControlState = w.view.Coverage.SourceStates["profiles"]
	evidence.ControlSubscriptions = len(w.profileSubs)
	evidence.State = "available"
	evidence.Message = fmt.Sprintf("核心 provider 识别 %d 项订阅；控制层配置 %d 项。两类来源独立，不将代理组当作机场。", evidence.CoreSubscriptions, evidence.ControlSubscriptions)
	if evidence.CoreState == "not_exposed" {
		evidence.Message = fmt.Sprintf("核心返回 %d 个 provider，其中 %d 个仅为组或内联视图，未提供可识别的机场订阅/额度；当前 %d 项配置订阅来自控制层。", evidence.ProviderEntries, evidence.EmbeddedProviders, evidence.ControlSubscriptions)
	}
	if evidence.CoreState == "unavailable" || evidence.CoreState == "waiting" {
		evidence.State = "partial"
		evidence.Message = fmt.Sprintf("核心 provider 尚未读取成功，机场订阅数量与额度未知；控制层配置清单保留 %d 项，来源独立。", evidence.ControlSubscriptions)
	}
	controlReadable := evidence.ControlState == "available"
	if !controlReadable && len(w.profileSubs) > 0 {
		evidence.State = "partial"
		evidence.Message += " 控制层当前不可读，保留上次配置清单与额度，不能视为最新数据。"
	} else if !controlReadable && w.settings.ControlBaseURL != "" {
		evidence.State = "partial"
		evidence.Message += " 控制层当前不可读，尚无可保留的配置清单；控制层订阅数量未知。"
	}
	if evidence.CoreState == "not_exposed" && !controlReadable {
		evidence.State = "unavailable"
		if len(w.profileSubs) == 0 {
			evidence.Message += " 控制层未配置或读取失败，机场订阅数量与额度未知，不能解释为零订阅。"
		}
	}
	if evidence.CoreState != "available" && !controlReadable && len(w.profileSubs) == 0 && len(w.settings.ImportedSubscriptions) == 0 {
		evidence.State = "unavailable"
		if evidence.CoreState != "not_exposed" {
			evidence.Message += " 控制层也不可读，不能解释为零订阅。"
		}
	}
}

// Retain only safe configuration metadata across restart, scoped to the exact
// service generation/settings revision. It never restores current health or
// asserts a core/profile association while the control source is unreadable.
func (w *networkWorker) restoreProfileSnapshot(snapshot NetworkView) {
	if w.settings.ControlBaseURL == "" || snapshot.ServiceID != w.rec.Service.ID || snapshot.Generation != w.rec.Service.Generation || snapshot.SettingsRevision != w.revision {
		return
	}
	for _, sub := range snapshot.Subscriptions {
		if sub.Source != "control" {
			continue
		}
		sub.Nodes = nil
		sub.NodeCountKnown = false
		w.profileSubs = append(w.profileSubs, sub)
	}
	if len(w.profileSubs) > 0 {
		w.view.Coverage.SourceStates["profiles"] = "waiting"
		w.view.Subscriptions = append([]NetworkSubscription{}, w.profileSubs...)
		w.updateSubscriptionSources()
	}
}
