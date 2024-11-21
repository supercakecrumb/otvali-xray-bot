package database

import (
	"errors"

	"gorm.io/gorm"
)

// ErrServerNotFound is returned when a server is not found in the database
var ErrServerNotFound = errors.New("server not found")

// AddServer adds a new server to the database
func (db *DB) AddServer(server *Server) error {
	return db.Conn.Create(server).Error
}

// UpdateServerInboundID updates the inbound ID of a server
func (db *DB) UpdateServerInboundID(serverID int64, inboundID int) error {
	return db.Conn.Model(&Server{}).Where("id = ?", serverID).Update("inbound_id", inboundID).Error
}

// GetServerByID retrieves a server by its ID
func (db *DB) GetServerByID(serverID int64) (*Server, error) {
	var server Server
	if err := db.Conn.First(&server, "id = ?", serverID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrServerNotFound
		}
		return nil, err
	}
	return &server, nil
}

// GetAllServers retrieves all servers from the database
func (db *DB) GetAllServers() ([]Server, error) {
	var servers []Server
	if err := db.Conn.Find(&servers).Error; err != nil {
		return nil, err
	}
	return servers, nil
}

// UpdateServerExclusivity updates whether a server is exclusive or not
func (db *DB) UpdateServerExclusivity(serverID int64, isExclusive bool) error {
	return db.Conn.Model(&Server{}).Where("id = ?", serverID).Update("is_exclusive", isExclusive).Error
}
