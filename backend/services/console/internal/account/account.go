// Package account is the shipper's own profile: the name, email and phone they signed up with, and an address
// book of places they send from or to. The address book is the console's alone; a change of the profile is
// announced (customer.updated) so back office's copy of the customer follows.
package account

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mseptiaan/loong.lat/backend/pkg/contracts"
	"github.com/mseptiaan/loong.lat/backend/pkg/eventbus"
	"github.com/mseptiaan/loong.lat/backend/pkg/platform"
	"github.com/mseptiaan/loong.lat/backend/services/console/internal/auth"
)

// SavedAddress is the frontend's SavedAddress.
type SavedAddress struct {
	ID           string             `json:"id"`
	Label        string             `json:"label"`
	Place        contracts.PlaceRef `json:"place"`
	ContactName  string             `json:"contactName"`
	ContactPhone string             `json:"contactPhone"`
}

// User is the frontend's User, as the customer sees themselves.
type User struct {
	ID             string         `json:"id"`
	Name           string         `json:"name"`
	Email          string         `json:"email"`
	Phone          string         `json:"phone"`
	CreatedAt      contracts.Time `json:"createdAt"`
	Blocked        bool           `json:"blocked"`
	SavedAddresses []SavedAddress `json:"savedAddresses"`
}

// Patch is the frontend's Partial<Pick<User, 'name' | 'phone' | 'email'>>: what is left out stays as it was.
type Patch struct {
	Name  *string `json:"name"`
	Phone *string `json:"phone"`
	Email *string `json:"email"`
}

// AddressInput is the frontend's Omit<SavedAddress, 'id'>.
type AddressInput struct {
	Label        string             `json:"label"`
	Place        contracts.PlaceRef `json:"place"`
	ContactName  string             `json:"contactName"`
	ContactPhone string             `json:"contactPhone"`
}

// AddressPatch is the frontend's Partial<Omit<SavedAddress, 'id'>>.
type AddressPatch struct {
	Label        *string             `json:"label"`
	Place        *contracts.PlaceRef `json:"place"`
	ContactName  *string             `json:"contactName"`
	ContactPhone *string             `json:"contactPhone"`
}

// Service keeps the profiles and address books.
type Service struct {
	pool *pgxpool.Pool
	pub  *eventbus.Publisher
	now  func() time.Time
}

func New(pool *pgxpool.Pool, pub *eventbus.Publisher) *Service {
	return &Service{pool: pool, pub: pub, now: time.Now}
}

func userNotFound() *platform.APIError {
	return platform.NotFound(contracts.Msg{Key: "srv.api.userNotFound"})
}

func addressNotFound() *platform.APIError {
	return platform.NotFound(contracts.Msg{Key: "srv.api.addressNotFound"})
}

func bad(field, key string) *platform.APIError {
	return platform.Invalid(field, contracts.Msg{Key: key})
}

// Profile is the customer's data with their address book.
func (s *Service) Profile(ctx context.Context, customerID string) (User, error) {
	return profile(ctx, s.pool, customerID)
}

type queryer interface {
	platform.Querier
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// profile reads the customer with their address book.
func profile(ctx context.Context, db queryer, customerID string) (User, error) {
	u := User{SavedAddresses: []SavedAddress{}}
	var created time.Time
	err := db.QueryRow(ctx, `SELECT id, name, email, phone, created_at, blocked FROM customers WHERE id = $1`, customerID).
		Scan(&u.ID, &u.Name, &u.Email, &u.Phone, &created, &u.Blocked)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, userNotFound()
	}
	if err != nil {
		return User{}, err
	}
	u.CreatedAt = contracts.At(created)
	rows, err := db.Query(ctx,
		`SELECT id, label, place, contact_name, contact_phone FROM saved_addresses WHERE customer_id = $1 ORDER BY seq`, customerID)
	if err != nil {
		return User{}, err
	}
	defer rows.Close()
	for rows.Next() {
		a, err := scanAddress(rows)
		if err != nil {
			return User{}, err
		}
		u.SavedAddresses = append(u.SavedAddresses, a)
	}
	return u, rows.Err()
}

func scanAddress(row pgx.Row) (SavedAddress, error) {
	var a SavedAddress
	var place []byte
	if err := row.Scan(&a.ID, &a.Label, &place, &a.ContactName, &a.ContactPhone); err != nil {
		return SavedAddress{}, err
	}
	if err := json.Unmarshal(place, &a.Place); err != nil {
		return SavedAddress{}, fmt.Errorf("saved address %s: %w", a.ID, err)
	}
	return a, nil
}

// pgUniqueViolation is Postgres' SQLSTATE for a unique constraint; the email is the only unique input.
const pgUniqueViolation = "23505"

// Update changes the customer's name, email or phone. The rules are registering's: a name must not be empty,
// the email must look like one and must not belong to another account, the phone needs enough digits. An email
// left empty is no change, as in the mock. When something did change, customer.updated carries the whole contact
// data, and the name shown for the customer's open sessions follows.
func (s *Service) Update(ctx context.Context, customerID string, patch Patch) (User, error) {
	var name, email, phone *string
	if patch.Name != nil {
		n := strings.TrimSpace(*patch.Name)
		if n == "" {
			return User{}, bad("name", "srv.api.nameEmpty")
		}
		name = &n
	}
	if patch.Email != nil && strings.TrimSpace(*patch.Email) != "" {
		e := strings.ToLower(strings.TrimSpace(*patch.Email))
		if !auth.ValidEmail(e) {
			return User{}, bad("email", "srv.api.emailInvalid")
		}
		email = &e
	}
	if patch.Phone != nil {
		ph := auth.NormalizePhone(*patch.Phone)
		if !auth.PhoneComplete(ph) {
			return User{}, bad("phone", "srv.api.phoneIncomplete")
		}
		phone = &ph
	}

	var out User
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		var cur struct {
			name, email, phone string
			version            int64
		}
		err := tx.QueryRow(ctx, `SELECT name, email, phone, version FROM customers WHERE id = $1 FOR UPDATE`, customerID).
			Scan(&cur.name, &cur.email, &cur.phone, &cur.version)
		if errors.Is(err, pgx.ErrNoRows) {
			return userNotFound()
		}
		if err != nil {
			return err
		}
		next := cur
		if name != nil {
			next.name = *name
		}
		if email != nil {
			next.email = *email
		}
		if phone != nil {
			next.phone = *phone
		}
		if next != cur {
			next.version++
			if _, err := tx.Exec(ctx, `UPDATE customers SET name = $2, email = $3, phone = $4, version = $5 WHERE id = $1`,
				customerID, next.name, next.email, next.phone, next.version); err != nil {
				return err
			}
			if next.name != cur.name {
				if _, err := tx.Exec(ctx, `UPDATE sessions SET name = $2 WHERE user_id = $1 AND role = $3`, customerID, next.name, string(contracts.RoleCustomer)); err != nil {
					return err
				}
			}
			err = s.pub.Enqueue(ctx, tx, eventbus.Event{
				Type: contracts.TypeCustomerUpdated, Key: customerID, AggregateVersion: next.version,
				Data: contracts.CustomerUpdated{CustomerID: customerID, Name: next.name, Email: next.email, Phone: next.phone},
			})
			if err != nil {
				return err
			}
		}
		out, err = profile(ctx, tx, customerID)
		return err
	})
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok && pgErr.Code == pgUniqueViolation {
		taken := platform.Conflict(contracts.Msg{Key: "srv.api.emailUsed"})
		taken.Field = "email"
		return User{}, taken.WithCause(err)
	}
	return out, err
}

// AddAddress puts a place in the address book. Only the label is required: it is what the customer picks it by.
func (s *Service) AddAddress(ctx context.Context, customerID string, in AddressInput) (SavedAddress, error) {
	label := strings.TrimSpace(in.Label)
	if label == "" {
		return SavedAddress{}, bad("label", "srv.api.addressLabelRequired")
	}
	a := SavedAddress{ID: "adr-" + uuid.NewV7().String(), Label: label, Place: in.Place, ContactName: in.ContactName, ContactPhone: in.ContactPhone}
	place, err := json.Marshal(a.Place)
	if err != nil {
		return SavedAddress{}, err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO saved_addresses (id, customer_id, label, place, contact_name, contact_phone, created_at) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		a.ID, customerID, a.Label, place, a.ContactName, a.ContactPhone, s.now())
	return a, err
}

// UpdateAddress changes what the patch names of one of the customer's addresses.
func (s *Service) UpdateAddress(ctx context.Context, customerID, id string, patch AddressPatch) (SavedAddress, error) {
	var label *string
	if patch.Label != nil {
		l := strings.TrimSpace(*patch.Label)
		if l == "" {
			return SavedAddress{}, bad("label", "srv.api.addressLabelRequired")
		}
		label = &l
	}
	var out SavedAddress
	err := platform.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		a, err := scanAddress(tx.QueryRow(ctx,
			`SELECT id, label, place, contact_name, contact_phone FROM saved_addresses WHERE id = $1 AND customer_id = $2 FOR UPDATE`, id, customerID))
		if errors.Is(err, pgx.ErrNoRows) {
			return addressNotFound()
		}
		if err != nil {
			return err
		}
		if label != nil {
			a.Label = *label
		}
		if patch.Place != nil {
			a.Place = *patch.Place
		}
		if patch.ContactName != nil {
			a.ContactName = *patch.ContactName
		}
		if patch.ContactPhone != nil {
			a.ContactPhone = *patch.ContactPhone
		}
		place, err := json.Marshal(a.Place)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE saved_addresses SET label = $2, place = $3, contact_name = $4, contact_phone = $5 WHERE id = $1`,
			a.ID, a.Label, place, a.ContactName, a.ContactPhone)
		out = a
		return err
	})
	return out, err
}

// RemoveAddress deletes one of the customer's addresses. Removing one that is not there is not an error: the
// address is gone either way.
func (s *Service) RemoveAddress(ctx context.Context, customerID, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM saved_addresses WHERE id = $1 AND customer_id = $2`, id, customerID)
	return err
}
