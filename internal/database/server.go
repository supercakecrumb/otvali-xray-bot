package database

func (db *DB) AddServer(server *Server) error {
	return db.Conn.Create(server).Error
}

func (db *DB) UpdateServerOutboundID(serverID int64, outboundID int) error {
	return db.Conn.Model(&Server{}).Where("id = ?", serverID).Update("outbound_id", outboundID).Error
}
