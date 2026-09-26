// Package scope carries who a request is being served for, and makes the
// database narrow itself to them.
//
// A reseller must never read, change or delete a customer that is not
// theirs. Getting that right by remembering to add a condition at every call
// site is not a strategy: this package has one place that decides whether a
// statement must be narrowed and one that narrows it, registered on the
// database handle the whole panel shares. A query that forgets is narrowed
// anyway, and so is one written next year by somebody who never read this.
//
// It sits below both the database and the service layer so that neither has
// to import the other to use it.
package scope

import (
	"context"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type key struct{}

// Scope is the operator a request is being served for.
//
// The zero value is unrestricted, which is what every internal caller gets:
// the reconciler, the subscription service, the Telegram bot and the CLI all
// work across the whole panel and none of them is acting for an operator.
type Scope struct {
	// AdminID is whose customers these are. Zero with Restricted set is the
	// owner's own customers, which is a real and different thing from "no
	// restriction" -- hence the separate flag rather than a sentinel.
	AdminID uint

	// Restricted narrows every customer and group query to AdminID.
	Restricted bool

	// Hidden is the group the owner puts this operator's customers in and
	// never shows them.
	//
	// It is carried here because it has to be stripped on the way out of
	// every list and put back on the way in to every write, and passing it
	// down as an argument would mean a path that forgot it would leak the
	// label the owner uses to tell whose customers are whose -- or, worse,
	// let the operator take a customer out of it.
	Hidden string

	// Interfaces is which tunnels this operator may put a customer on. Nil
	// is no restriction, which is what the owner and a panel administrator
	// get; an empty map is a reseller who has been given none, and is a
	// real state -- one kept on the panel with nothing left to sell.
	Interfaces map[uint]bool

	// ClientLimit is how many customers this operator may hold at once.
	// Zero is no limit.
	ClientLimit int

	// Barred, when set, says why this operator may not create anything
	// right now: switched off, term ended, or allowance gone. Their
	// existing customers are stopped by the same facts, computed where
	// service is decided; this is the half that has to be said out loud,
	// because an operator whose form silently failed would open a support
	// ticket rather than pay for more.
	Barred string
}

// Allows reports whether this operator may sell the given tunnel.
func (s Scope) Allows(interfaceID uint) bool {
	if s.Interfaces == nil {
		return true
	}
	return s.Interfaces[interfaceID]
}

// Hides reports whether a group name is the one kept from this operator.
func (s Scope) Hides(name string) bool {
	return s.Restricted && s.Hidden != "" && strings.EqualFold(name, s.Hidden)
}

// Visible drops the hidden group from a list of names.
func (s Scope) Visible(names []string) []string {
	if !s.Restricted || s.Hidden == "" {
		return names
	}
	out := names[:0]
	for _, n := range names {
		if !s.Hides(n) {
			out = append(out, n)
		}
	}
	return out
}

// With attaches a scope to a context. Everything below reads it from there
// rather than taking it as an argument, so a call site cannot silently skip
// it by not being updated.
func With(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, key{}, s)
}

// Of reads the scope back. An unscoped context is unrestricted.
func Of(ctx context.Context) Scope {
	s, _ := ctx.Value(key{}).(Scope)
	return s
}

// OwnerOf is the id to stamp on something this request creates.
func OwnerOf(ctx context.Context) uint { return Of(ctx).AdminID }

// Narrow applies the caller's own-customers condition to a query builder.
//
// Used where the table is not the one Register can see -- a count over
// accounts, a group aggregate -- with the column spelled out. Register and
// this apply the same condition, and applying it twice is harmless.
func Narrow(ctx context.Context, q *gorm.DB, column string) *gorm.DB {
	s := Of(ctx)
	if !s.Restricted {
		return q
	}
	return q.Where(column+" = ?", s.AdminID)
}

// Register makes the narrowing automatic for the customers table.
//
// Every query, update and delete that names it gets "owner_id = ?" appended
// when the context carries a restricted scope. This is the backstop: a
// handler that forgets, a helper added later, a path nobody thought about --
// all narrowed anyway, and the only way to reach another operator's
// customers is to run with no scope at all, which no request from a browser
// ever does.
func Register(db *gorm.DB) error {
	narrow := func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Context == nil {
			return
		}
		s := Of(tx.Statement.Context)
		if !s.Restricted {
			return
		}
		if tx.Statement.Table != "clients" &&
			(tx.Statement.Schema == nil || tx.Statement.Schema.Table != "clients") {
			return
		}
		tx.Statement.AddClause(clause.Where{Exprs: []clause.Expression{
			clause.Eq{
				Column: clause.Column{Table: "clients", Name: "owner_id"},
				Value:  s.AdminID,
			},
		}})
	}

	if err := db.Callback().Query().Before("gorm:query").
		Register("wui:scope_query", narrow); err != nil {
		return err
	}
	if err := db.Callback().Update().Before("gorm:update").
		Register("wui:scope_update", narrow); err != nil {
		return err
	}
	if err := db.Callback().Delete().Before("gorm:delete").
		Register("wui:scope_delete", narrow); err != nil {
		return err
	}
	return nil
}
