package api

import (
	"sync"

	"gorm.io/gorm"
)

// The server holds no database; the tests that seed one next to a server
// keep it here, by the server it belongs to.
var testDBs sync.Map

func withDB(s *Server, db *gorm.DB) *Server {
	testDBs.Store(s, db)
	return s
}

func dbOf(s *Server) *gorm.DB {
	db, _ := testDBs.Load(s)
	return db.(*gorm.DB)
}
