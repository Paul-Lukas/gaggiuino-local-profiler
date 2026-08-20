package machines

import (
	"context"
	"encoding/json"
)

// This file ports lib/machines/gaggimate/profiles.js: thin pass-throughs
// over gaggimate_ws.go's request() for GaggiMate's own profile JSON shape
// (req:profiles:list/load/save/delete/select) — GLP exposes GaggiMate
// profiles read-only (capabilities().profileEdit == false, see
// gaggimate_adapter.go), same as Node.

func gaggimateListProfiles(ctx context.Context, baseURL string) ([]ProfileSummary, error) {
	res, err := gaggimateRequest(ctx, baseURL, "req:profiles:list", nil)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(res["profiles"])
	if err != nil {
		return []ProfileSummary{}, nil
	}
	var profiles []ProfileSummary
	if err := json.Unmarshal(raw, &profiles); err != nil {
		return []ProfileSummary{}, nil
	}
	return profiles, nil
}

func gaggimateLoadProfile(ctx context.Context, baseURL string, id int) (json.RawMessage, error) {
	res, err := gaggimateRequest(ctx, baseURL, "req:profiles:load", map[string]any{"id": id})
	if err != nil {
		return nil, err
	}
	if profile, ok := res["profile"]; ok {
		return json.Marshal(profile)
	}
	return json.Marshal(res)
}

// gaggimateSaveProfile ports saveProfile(baseUrl, profile) — used for both
// create and update (profiles.js's saveProfile is the same call either
// way; GaggiMate's own req:profiles:save has no separate create/update
// distinction). profile is passed through as GaggiMate's own JSON profile
// shape, not converted through Gaggiuino's ProfileInput/proto.ProfileDto —
// see gaggimate_adapter.go's CreateProfile/UpdateProfile doc comment.
func gaggimateSaveProfile(ctx context.Context, baseURL string, profile json.RawMessage) (ProfileSummary, error) {
	var decoded any
	if err := json.Unmarshal(profile, &decoded); err != nil {
		return ProfileSummary{}, err
	}
	res, err := gaggimateRequest(ctx, baseURL, "req:profiles:save", map[string]any{"profile": decoded})
	if err != nil {
		return ProfileSummary{}, err
	}
	body := res
	if p, ok := res["profile"]; ok {
		if m, ok := p.(map[string]any); ok {
			body = m
		}
	}
	var summary ProfileSummary
	raw, err := json.Marshal(body)
	if err != nil {
		return ProfileSummary{}, err
	}
	_ = json.Unmarshal(raw, &summary)
	return summary, nil
}

func gaggimateDeleteProfile(ctx context.Context, baseURL string, id int) error {
	_, err := gaggimateRequest(ctx, baseURL, "req:profiles:delete", map[string]any{"id": id})
	return err
}

func gaggimateSelectProfile(ctx context.Context, baseURL string, id int) error {
	_, err := gaggimateRequest(ctx, baseURL, "req:profiles:select", map[string]any{"id": id})
	return err
}
