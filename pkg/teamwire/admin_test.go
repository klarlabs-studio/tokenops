package teamwire

import (
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"
)

// adminTypes are the self-serve messages. None may carry figures: those
// cross only in an Upload, under its rules.
var adminTypes = []any{
	DeviceLinkRequest{}, DeviceLink{}, DeviceLinkPoll{}, DeviceLinkResult{}, AdminCredential{},
	Team{}, CreateTeamRequest{}, Member{}, InviteRequest{}, Invite{}, RoleRequest{}, GrantRequest{},
	Grant{}, Created{}, AuditEntry{}, Plan{}, Billing{}, BillingLink{},
}

// TestAdminMessagesCarryNoFigures fails when an administration message
// reaches an Upload or a Bucket: figures leave a machine only in an
// upload, through Validate and TestUploadHasNoFreeText.
func TestAdminMessagesCarryNoFigures(t *testing.T) {
	forbidden := map[reflect.Type]bool{reflect.TypeOf(Upload{}): true, reflect.TypeOf(Bucket{}): true}
	var walk func(path string, typ reflect.Type)
	walk = func(path string, typ reflect.Type) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		if forbidden[typ] {
			t.Errorf("%s reaches %s; figures cross only in an Upload", path, typ.Name())
			return
		}
		if typ.Kind() != reflect.Struct || typ == reflect.TypeOf(time.Time{}) {
			return
		}
		for i := range typ.NumField() {
			f := typ.Field(i)
			walk(path+"."+f.Name, f.Type)
		}
	}
	for _, v := range adminTypes {
		typ := reflect.TypeOf(v)
		walk(typ.Name(), typ)
	}
}

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// The administration API's field names are what deployed servers answer
// with; a rename breaks the CLI against them.
func TestAdminJSONNames(t *testing.T) {
	now := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		v    any
		want []string
	}{
		{Team{ID: "t", Name: "n", Members: 1}, []string{"id", "members", "name"}},
		{Member{ID: "m", Name: "n", Role: RoleLead, Teams: []string{}, Email: "e", CanView: true, IsYou: true},
			[]string{"can_view", "email", "id", "is_you", "name", "role", "teams"}},
		{Invite{Invite: "i", ExpiresAt: now, Join: "j", Emailed: true}, []string{"emailed", "expires_at", "invite", "join"}},
		{InviteRequest{Team: "t", Role: RoleMember, TTLHours: 1, Email: "e"}, []string{"email", "role", "team", "ttl_hours"}},
		{GrantRequest{GranteeID: "g", Team: "t", Reason: "r"}, []string{"grantee_id", "reason", "team"}},
		{Grant{ID: "g", RevokedAt: &now}, []string{"granted_at", "granted_by", "grantee", "grantee_id", "id", "reason", "revoked_at", "scope"}},
		{AuditEntry{At: now, Actor: "a", Action: "x", Subject: "s", Detail: "d"}, []string{"action", "actor", "at", "detail", "subject"}},
		{DeviceLink{DeviceCode: "d", UserCode: "u", VerificationURL: "v", ExpiresAt: now, Interval: 5},
			[]string{"device_code", "expires_at", "interval", "user_code", "verification_url"}},
		{Billing{Plan: Plan{Status: PlanTrialing, TrialEndsAt: &now}, Seats: 3, ManageURL: "u"},
			[]string{"manage_url", "seats", "status", "trial_ends_at", "uploads_paused"}},
	}
	cases[5].want = []string{"granted_at", "granted_by", "grantee", "grantee_id", "id", "reason", "revoked_at", "scope"}
	for _, c := range cases {
		got := jsonKeys(t, c.v)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%T JSON keys = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestValidRole(t *testing.T) {
	for _, r := range Roles {
		if !ValidRole(r) {
			t.Errorf("%s refused", r)
		}
	}
	if ValidRole("superuser") || ValidRole("") {
		t.Error("accepted a role that does not exist")
	}
}
