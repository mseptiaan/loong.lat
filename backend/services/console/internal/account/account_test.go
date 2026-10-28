package account

import (
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/testkit"
)

func newService(t *testing.T) (*Service, *pgxpool.Pool, string) {
	t.Helper()
	pool := testkit.DB(t)
	return New(pool, testkit.Publisher(pool)), pool, testkit.Customer(t, pool, "Dina Pramesti")
}

func wantError(t *testing.T, err error, code platform.ErrorCode, key, field string) {
	t.Helper()
	got, ok := errors.AsType[*platform.APIError](err)
	if !ok {
		t.Fatalf("want a platform.APIError, got %T: %v", err, err)
	}
	if got.Code != code || got.Msg.Key != key || got.Field != field {
		t.Fatalf("got %s %q field %q, want %s %q field %q", got.Code, got.Msg.Key, got.Field, code, key, field)
	}
}

func ptr(s string) *string { return &s }

func home() AddressInput {
	return AddressInput{
		Label: "Home", ContactName: "Dina", ContactPhone: "081234567890",
		Place: contracts.PlaceRef{Name: "Kemang Raya 12", Address: "Jl. Kemang Raya 12", Position: contracts.LatLng{Lat: -6.26, Lng: 106.81}, PlaceID: "pl-1"},
	}
}

func TestTheProfileIsTheCustomerWithAnAddressBookThatIsAListEvenWhenEmpty(t *testing.T) {
	s, _, id := newService(t)
	u, err := s.Profile(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID != id || u.Name != "Dina Pramesti" || u.Email != id+"@example.test" || u.Phone != "081234567890" || u.Blocked || u.CreatedAt.IsZero() {
		t.Errorf("profile = %+v", u)
	}
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"savedAddresses":[]`) || !strings.Contains(string(raw), `"blocked":false`) {
		t.Errorf("wire form = %s: the address book must be [] and not null", raw)
	}
	if _, err := s.Profile(t.Context(), "cus-ghost"); err == nil {
		t.Error("a customer that does not exist has a profile")
	}
}

func TestUpdatingTheProfileNormalizesItAndAnnouncesTheNewContactData(t *testing.T) {
	s, pool, id := newService(t)
	sessions := platform.NewPgSessionStore(pool)
	hash := platform.HashToken("dina-token")
	if err := sessions.Create(t.Context(), hash, platform.Principal{Role: contracts.RoleCustomer, UserID: id, Name: "Dina Pramesti"}, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	u, err := s.Update(t.Context(), id, Patch{Name: ptr("  Dina P.  "), Email: ptr(" Dina.P@Contoh.ID "), Phone: ptr("+62 812-9999-0001")})
	if err != nil {
		t.Fatal(err)
	}
	if u.Name != "Dina P." || u.Email != "dina.p@contoh.id" || u.Phone != "081299990001" {
		t.Errorf("profile = %+v, want the trimmed name, lower-cased email and the local phone form", u)
	}

	envs := testkit.Outbox(t, pool, contracts.TypeCustomerUpdated)
	if len(envs) != 1 || envs[0].Key != id || envs[0].AggregateVersion != 2 {
		t.Fatalf("customer.updated = %+v: registering was version 1, this is 2", envs)
	}
	ev, err := envs[0].Payload[contracts.CustomerUpdated]()
	if err != nil || ev != (contracts.CustomerUpdated{CustomerID: id, Name: "Dina P.", Email: "dina.p@contoh.id", Phone: "081299990001"}) {
		t.Errorf("payload = %+v, %v: the whole contact data", ev, err)
	}
	if p, err := platform.NewSessions(sessions, time.Hour, testkit.Log).Resolve(t.Context(), "dina-token"); err != nil || p.Name != "Dina P." {
		t.Errorf("the open session shows %+v (%v), want the new name", p, err)
	}

	// Only one field: the rest stays, the version moves by one.
	u, err = s.Update(t.Context(), id, Patch{Name: ptr("Dina Pramesti")})
	if err != nil || u.Name != "Dina Pramesti" || u.Email != "dina.p@contoh.id" {
		t.Fatalf("second update = %+v, %v", u, err)
	}
	if envs := testkit.Outbox(t, pool, contracts.TypeCustomerUpdated); len(envs) != 2 || envs[1].AggregateVersion != 3 {
		t.Errorf("customer.updated events = %d, last version %d", len(envs), envs[len(envs)-1].AggregateVersion)
	}
}

func TestAnUpdateThatChangesNothingPublishesNothing(t *testing.T) {
	s, pool, id := newService(t)
	for name, patch := range map[string]Patch{
		"an empty patch":        {},
		"the same values":       {Name: ptr("Dina Pramesti"), Phone: ptr("0812 3456 7890")},
		"the same email, cased": {Email: ptr(strings.ToUpper(id + "@example.test"))},
		"an empty email":        {Email: ptr("")}, // as in the mock: no change
	} {
		if _, err := s.Update(t.Context(), id, patch); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if n := len(testkit.OutboxTypes(t, pool)); n != 0 {
		t.Errorf("%d events for updates that changed nothing", n)
	}
	if v := testkit.Count(t, pool, `SELECT version FROM customers WHERE id = $1`, id); v != 1 {
		t.Errorf("version = %d", v)
	}
}

func TestAnUpdateIsRefusedLikeRegisteringIs(t *testing.T) {
	s, pool, id := newService(t)
	other := testkit.Customer(t, pool, "Raka")
	var otherEmail string
	if err := pool.QueryRow(t.Context(), `SELECT email FROM customers WHERE id = $1`, other).Scan(&otherEmail); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name  string
		patch Patch
		code  platform.ErrorCode
		key   string
		field string
	}{
		{"a blank name", Patch{Name: ptr("   ")}, platform.CodeInvalid, "srv.api.nameEmpty", "name"},
		{"an email that is none", Patch{Email: ptr("dina@")}, platform.CodeInvalid, "srv.api.emailInvalid", "email"},
		{"a short phone", Patch{Phone: ptr("0812")}, platform.CodeInvalid, "srv.api.phoneIncomplete", "phone"},
		{"another account's email", Patch{Email: ptr(strings.ToUpper(otherEmail))}, platform.CodeConflict, "srv.api.emailUsed", "email"},
		{"a valid name with another account's email", Patch{Name: ptr("New Name"), Email: ptr(otherEmail)}, platform.CodeConflict, "srv.api.emailUsed", "email"},
	} {
		_, err := s.Update(t.Context(), id, tc.patch)
		wantError(t, err, tc.code, tc.key, tc.field)
		u, perr := s.Profile(t.Context(), id)
		if perr != nil || u.Name != "Dina Pramesti" || u.Email != id+"@example.test" || u.Phone != "081234567890" {
			t.Errorf("%s: the refused update changed the profile: %+v", tc.name, u)
		}
	}
	if n := len(testkit.OutboxTypes(t, pool)); n != 0 {
		t.Errorf("%d events published by refused updates", n)
	}
	if _, err := s.Update(t.Context(), "cus-ghost", Patch{Name: ptr("x")}); err == nil {
		t.Error("a customer that does not exist was updated")
	}
}

func TestTheAddressBookKeepsAddressesInOrderAndOnlyForTheirOwner(t *testing.T) {
	s, pool, id := newService(t)
	other := testkit.Customer(t, pool, "Raka")
	ctx := t.Context()

	first, err := s.AddAddress(ctx, id, home())
	if err != nil {
		t.Fatal(err)
	}
	office := home()
	office.Label, office.Place.Name = "  Office ", "SCBD"
	second, err := s.AddAddress(ctx, id, office)
	if err != nil {
		t.Fatal(err)
	}
	third, _ := s.AddAddress(ctx, id, AddressInput{Label: "Warehouse"})
	theirs, _ := s.AddAddress(ctx, other, AddressInput{Label: "Raka's"})

	if !strings.HasPrefix(first.ID, "adr-") || first.ID == second.ID || first.Label != "Home" || second.Label != "Office" {
		t.Errorf("added = %+v / %+v", first, second)
	}
	u, err := s.Profile(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(u.SavedAddresses); !slices.Equal(got, []string{first.ID, second.ID, third.ID}) {
		t.Fatalf("address book = %v, want the order they were added in and none of Raka's", got)
	}
	if u.SavedAddresses[0] != first || u.SavedAddresses[0].Place.PlaceID != "pl-1" || u.SavedAddresses[0].ContactName != "Dina" {
		t.Errorf("the stored address %+v differs from what was added %+v", u.SavedAddresses[0], first)
	}

	if _, err := s.AddAddress(ctx, id, AddressInput{Label: "  "}); err == nil {
		t.Error("an address without a label was added")
	} else {
		wantError(t, err, platform.CodeInvalid, "srv.api.addressLabelRequired", "label")
	}

	// Update: only what the patch names; somebody else's address is not found.
	updated, err := s.UpdateAddress(ctx, id, second.ID, AddressPatch{Label: ptr("HQ"), ContactPhone: ptr("0811")})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Label != "HQ" || updated.ContactPhone != "0811" || updated.ContactName != "Dina" || updated.Place.Name != "SCBD" {
		t.Errorf("updated = %+v", updated)
	}
	moved := contracts.PlaceRef{Name: "Senayan", Address: "Jl. Asia Afrika", Position: contracts.LatLng{Lat: -6.22, Lng: 106.8}}
	updated, err = s.UpdateAddress(ctx, id, second.ID, AddressPatch{Place: &moved})
	if err != nil || updated.Place != moved || updated.Label != "HQ" {
		t.Errorf("moving the place: %+v, %v", updated, err)
	}
	_, err = s.UpdateAddress(ctx, id, second.ID, AddressPatch{Label: ptr(" ")})
	wantError(t, err, platform.CodeInvalid, "srv.api.addressLabelRequired", "label")
	_, err = s.UpdateAddress(ctx, id, theirs.ID, AddressPatch{Label: ptr("mine now")})
	wantError(t, err, platform.CodeNotFound, "srv.api.addressNotFound", "")
	_, err = s.UpdateAddress(ctx, id, "adr-ghost", AddressPatch{})
	wantError(t, err, platform.CodeNotFound, "srv.api.addressNotFound", "")
	if n := testkit.Count(t, pool, `SELECT count(*) FROM saved_addresses WHERE customer_id = $1 AND label = 'mine now'`, other); n != 0 {
		t.Error("somebody else's address was changed")
	}

	// Remove: one of mine goes, somebody else's stays, and one that is not there is no error.
	if err := s.RemoveAddress(ctx, id, first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveAddress(ctx, id, first.ID); err != nil {
		t.Errorf("removing it again: %v", err)
	}
	if err := s.RemoveAddress(ctx, id, theirs.ID); err != nil {
		t.Fatal(err)
	}
	u, _ = s.Profile(ctx, id)
	if got := ids(u.SavedAddresses); !slices.Equal(got, []string{second.ID, third.ID}) {
		t.Errorf("address book after removing = %v", got)
	}
	if o, _ := s.Profile(ctx, other); len(o.SavedAddresses) != 1 {
		t.Errorf("Raka's address book has %d entries: removing by someone else's id must not delete it", len(o.SavedAddresses))
	}
	if n := len(testkit.OutboxTypes(t, pool)); n != 0 {
		t.Errorf("%d events: the address book is nobody else's business", n)
	}
}

func ids(list []SavedAddress) []string {
	out := []string{}
	for _, a := range list {
		out = append(out, a.ID)
	}
	return out
}
