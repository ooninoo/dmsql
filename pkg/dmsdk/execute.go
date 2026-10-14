package dm

import "context"

// OpenConnection connects using the initialized, registered DM driver.
func OpenConnection(ctx context.Context, dsn string) (*DmConnection, error) {
	connector, err := globalDmDriver.OpenConnector(dsn)
	if err != nil {
		return nil, err
	}
	conn, err := connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return conn.(*DmConnection), nil
}

// ExecuteContext executes SQL once, returning either rows or an update result.
// The caller must close returned rows before executing another statement.
func (dc *DmConnection) ExecuteContext(ctx context.Context, query string) (interface{}, error) {
	if err := dc.checkClosed(); err != nil {
		return nil, err
	}
	if err := dc.watchCancel(ctx); err != nil {
		return nil, err
	}
	keepWatching := false
	defer func() {
		if !keepWatching {
			dc.finish()
		}
	}()
	value, err := dc.executeInner(query, Dm_build_791)
	if err != nil {
		return nil, err
	}
	if result, ok := value.(*DmResult); ok {
		if err := result.dmStmt.Close(); err != nil {
			return nil, err
		}
	}
	if rows, ok := value.(*DmRows); ok {
		rows.finish = dc.finish
		keepWatching = true
	}
	return value, nil
}
