package database

func (db *DB) AddServer(server *Server) error {
	return db.Conn.Create(server).Error
}

func (db *DB) UpdateServerInboundID(serverID int64, inboundID int) error {
	return db.Conn.Model(&Server{}).Where("id = ?", serverID).Update("inbound_id", inboundID).Error
}
