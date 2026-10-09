package model

// S3EventNotification represents the standard S3 object created event JSON
type S3EventNotification struct {
	Records []struct {
		S3 struct {
			Bucket struct {
				Name string `json:"name"`
			} `json:"bucket"`
			Object struct {
				Key string `json:"key"`
			} `json:"object"`
		} `json:"s3"`
	} `json:"Records"`
}

// DASPayload represents the schema inside the Firehose / S3 object for DAS
type DASPayload struct {
	Type                   string `json:"type"`
	Version                string `json:"version"`
	DatabaseActivityEvents string `json:"databaseActivityEvents"`
	Key                    string `json:"key"`
}

// DatabaseActivityEvent represents a single DAS event formatted for Parquet serialization
type DatabaseActivityEvent struct {
	LogTime           string   `parquet:"logTime,dict" json:"logTime"`
	StatementId       int64    `parquet:"statementId" json:"statementId"`
	SubstatementId    int64    `parquet:"substatementId" json:"substatementId"`
	ObjectType        string   `parquet:"objectType,dict" json:"objectType"`
	Command           string   `parquet:"command,dict" json:"command"`
	ObjectName        string   `parquet:"objectName,dict" json:"objectName"`
	DatabaseName      string   `parquet:"databaseName,dict" json:"databaseName"`
	DbUserName        string   `parquet:"dbUserName,dict" json:"dbUserName"`
	RemoteHost        string   `parquet:"remoteHost,dict" json:"remoteHost"`
	SessionId         string   `parquet:"sessionId,dict" json:"sessionId"`
	RowCount          int64    `parquet:"rowCount" json:"rowCount"`
	CommandText       string   `parquet:"commandText,dict" json:"commandText"`
	ParamList         []string `parquet:"paramList,list" json:"paramList"`
	Pid               int64    `parquet:"pid" json:"pid"`
	ClientApplication string   `parquet:"clientApplication,dict" json:"clientApplication"`
	ExitCode          string   `parquet:"exitCode,dict" json:"exitCode"`
	Class             string   `parquet:"class,dict" json:"class"`
	ServerHost        string   `parquet:"serverHost,dict" json:"serverHost"`
	Type              string   `parquet:"type,dict" json:"type"`
	StartTime         string   `parquet:"startTime,dict" json:"startTime"`
	ErrorMessage      string   `parquet:"errorMessage,dict" json:"errorMessage"`
}
