package autopilot

// ManualTargetAvailable rejects a mapped target only when this exact endpoint/key
// has a nonempty discovered inventory that does not contain it. Missing inventories
// are not proof of unavailability and must permit a real upstream attempt.
func ManualTargetAvailable(store *ProfileStore, channelUID, baseURL, apiKey, target string) bool {
	if store == nil || channelUID == "" {
		return true
	}
	profile := store.Get(GenerateEndpointUID(channelUID, baseURL, KeyHashFromAPIKey(apiKey)))
	if profile == nil || len(profile.AvailableModels) == 0 {
		return true
	}
	for _, available := range profile.AvailableModels {
		if available == target {
			return true
		}
	}
	return false
}
