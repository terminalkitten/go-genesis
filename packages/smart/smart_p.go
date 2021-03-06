// Copyright 2016 The go-daylight Authors
// This file is part of the go-daylight library.
//
// The go-daylight library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-daylight library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-daylight library. If not, see <http://www.gnu.org/licenses/>.

package smart

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/GenesisKernel/go-genesis/packages/conf/syspar"
	"github.com/GenesisKernel/go-genesis/packages/consts"
	"github.com/GenesisKernel/go-genesis/packages/converter"
	"github.com/GenesisKernel/go-genesis/packages/crypto"
	"github.com/GenesisKernel/go-genesis/packages/language"
	"github.com/GenesisKernel/go-genesis/packages/model"
	"github.com/GenesisKernel/go-genesis/packages/script"
	"github.com/GenesisKernel/go-genesis/packages/utils"
	"github.com/GenesisKernel/go-genesis/packages/utils/metric"

	"github.com/shopspring/decimal"
	log "github.com/sirupsen/logrus"
)

var (
	funcCallsDBP = map[string]struct{}{
		"DBInsert":         {},
		"DBUpdate":         {},
		"DBUpdateSysParam": {},
		"DBUpdateExt":      {},
		"DBSelect":         {},
	}

	extendCostSysParams = map[string]string{
		"AddressToId":       "extend_cost_address_to_id",
		"IdToAddress":       "extend_cost_id_to_address",
		"NewState":          "extend_cost_new_state",
		"Sha256":            "extend_cost_sha256",
		"PubToID":           "extend_cost_pub_to_id",
		"EcosysParam":       "extend_cost_ecosys_param",
		"SysParamString":    "extend_cost_sys_param_string",
		"SysParamInt":       "extend_cost_sys_param_int",
		"SysFuel":           "extend_cost_sys_fuel",
		"ValidateCondition": "extend_cost_validate_condition",
		"EvalCondition":     "extend_cost_eval_condition",
		"HasPrefix":         "extend_cost_has_prefix",
		"Contains":          "extend_cost_contains",
		"Replace":           "extend_cost_replace",
		"Join":              "extend_cost_join",
		"Size":              "extend_cost_size",
		"Substr":            "extend_cost_substr",
		"Eval":              "extend_cost_eval",
		"Len":               "extend_cost_len",
		"Activate":          "extend_cost_activate",
		"Deactivate":        "extend_cost_deactivate",
		"CreateEcosystem":   "extend_cost_create_ecosystem",
		"TableConditions":   "extend_cost_table_conditions",
		"CreateTable":       "extend_cost_create_table",
		"PermTable":         "extend_cost_perm_table",
		"ColumnCondition":   "extend_cost_column_condition",
		"CreateColumn":      "extend_cost_create_column",
		"PermColumn":        "extend_cost_perm_column",
		"JSONToMap":         "extend_cost_json_to_map",
		"GetContractByName": "extend_cost_contract_by_name",
		"GetContractById":   "extend_cost_contract_by_id",
	}
)

const (
	nActivateContract   = "ActivateContract"
	nDeactivateContract = "DeactivateContract"
	nEditContract       = "EditContract"
	nImport             = "Import"
	nNewContract        = "NewContract"
)

//SignRes contains the data of the signature
type SignRes struct {
	Param string `json:"name"`
	Text  string `json:"text"`
}

// TxSignJSON is a structure for additional signs of transaction
type TxSignJSON struct {
	ForSign string    `json:"forsign"`
	Field   string    `json:"field"`
	Title   string    `json:"title"`
	Params  []SignRes `json:"params"`
}

func init() {
	EmbedFuncs(smartVM, script.VMTypeSmart)
}

func getCostP(name string) int64 {
	if key, ok := extendCostSysParams[name]; ok && syspar.HasSys(key) {
		return syspar.SysInt64(key)
	}
	return -1
}

// UpdateSysParam updates the system parameter
func UpdateSysParam(sc *SmartContract, name, value, conditions string) (int64, error) {
	var (
		fields []string
		values []interface{}
	)
	par := &model.SystemParameter{}
	found, err := par.Get(name)
	if err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("system parameter get")
		return 0, err
	}
	if !found {
		log.WithFields(log.Fields{"type": consts.NotFound, "error": err}).Error("system parameter get")
		return 0, fmt.Errorf(`Parameter %s has not been found`, name)
	}
	cond := par.Conditions
	if len(cond) > 0 {
		ret, err := sc.EvalIf(cond)
		if err != nil {
			log.WithFields(log.Fields{"type": consts.EvalError, "error": err}).Error("evaluating conditions")
			return 0, err
		}
		if !ret {
			log.WithFields(log.Fields{"type": consts.AccessDenied}).Error("Access denied")
			return 0, errAccessDenied
		}
	}
	if len(value) > 0 {
		var (
			ok, checked bool
			list        [][]string
		)
		ival := converter.StrToInt64(value)
	check:
		switch name {
		case `gap_between_blocks`:
			ok = ival > 0 && ival < 86400
		case `rb_blocks_1`, `number_of_nodes`:
			ok = ival > 0 && ival < 1000
		case `ecosystem_price`, `contract_price`, `column_price`, `table_price`, `menu_price`,
			`page_price`, `commission_size`:
			ok = ival >= 0
		case `max_block_size`, `max_tx_size`, `max_tx_count`, `max_columns`, `max_indexes`,
			`max_block_user_tx`, `max_fuel_tx`, `max_fuel_block`, `max_forsign_size`:
			ok = ival > 0
		case `fuel_rate`, `commission_wallet`:
			err := json.Unmarshal([]byte(value), &list)
			if err != nil {
				log.WithFields(log.Fields{"type": consts.JSONUnmarshallError, "error": err}).Error("unmarshalling system param")
				return 0, err
			}
			for _, item := range list {
				switch name {
				case `fuel_rate`, `commission_wallet`:
					if len(item) != 2 || converter.StrToInt64(item[0]) <= 0 ||
						(name == `fuel_rate` && converter.StrToInt64(item[1]) <= 0) ||
						(name == `commission_wallet` && converter.StrToInt64(item[1]) == 0) {
						break check
					}
				}
			}
			checked = true
		case syspar.FullNodes:
			fnodes := []syspar.FullNode{}
			if err := json.Unmarshal([]byte(value), &fnodes); err != nil {
				break check
			}
			checked = len(fnodes) > 0
		default:
			if strings.HasPrefix(name, `extend_cost_`) {
				ok = ival >= 0
				break
			}
			checked = true
		}
		if !checked && (!ok || converter.Int64ToStr(ival) != value) {
			log.WithFields(log.Fields{"type": consts.InvalidObject, "value": value, "name": name}).Error(ErrInvalidValue.Error())
			return 0, ErrInvalidValue
		}
		fields = append(fields, "value")
		values = append(values, value)
	}
	if len(conditions) > 0 {
		if err := CompileEval(conditions, 0); err != nil {
			log.WithFields(log.Fields{"error": err, "conditions": conditions, "state_id": 0, "type": consts.EvalError}).Error("compiling eval")
			return 0, err
		}
		fields = append(fields, "conditions")
		values = append(values, conditions)
	}
	if len(fields) == 0 {
		log.WithFields(log.Fields{"type": consts.EmptyObject}).Error("empty value and condition")
		return 0, fmt.Errorf(`empty value and condition`)
	}
	_, _, err = sc.selectiveLoggingAndUpd(fields, values, "1_system_parameters", []string{"id"}, []string{converter.Int64ToStr(par.ID)}, !sc.VDE && sc.Rollback, false)
	if err != nil {
		return 0, err
	}
	err = syspar.SysUpdate(sc.DbTransaction)
	if err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("updating syspar")
		return 0, err
	}
	sc.SysUpdate = true
	return 0, nil
}

// DBUpdateExt updates the record in the specified table. You can specify 'where' query in params and then the values for this query
func DBUpdateExt(sc *SmartContract, tblname string, column string, value interface{},
	params string, val ...interface{}) (qcost int64, err error) {
	tblname = getDefTableName(sc, tblname)
	if err = sc.AccessTable(tblname, "update"); err != nil {
		return
	}
	if strings.Contains(tblname, `_reports_`) {
		err = fmt.Errorf(`Access denied to report table`)
		return
	}
	columns := strings.Split(params, `,`)
	if err = sc.AccessColumns(tblname, &columns, true); err != nil {
		return
	}
	qcost, _, err = sc.selectiveLoggingAndUpd(columns, val, tblname, []string{column}, []string{fmt.Sprint(value)}, !sc.VDE && sc.Rollback, true)
	return
}

// SysParamString returns the value of the system parameter
func SysParamString(name string) string {
	return syspar.SysString(name)
}

// SysParamInt returns the value of the system parameter
func SysParamInt(name string) int64 {
	return syspar.SysInt64(name)
}

// SysFuel returns the fuel rate
func SysFuel(state int64) string {
	return syspar.GetFuelRate(state)
}

// Int converts the value to a number
func Int(v interface{}) (int64, error) {
	return converter.ValueToInt(v)
}

// Str converts the value to a string
func Str(v interface{}) (ret string) {
	switch val := v.(type) {
	case float64:
		ret = fmt.Sprintf(`%f`, val)
	default:
		ret = fmt.Sprintf(`%v`, val)
	}
	return
}

// Money converts the value into a numeric type for money
func Money(v interface{}) (decimal.Decimal, error) {
	return script.ValueToDecimal(v)
}

// Float converts the value to float64
func Float(v interface{}) (ret float64) {
	return script.ValueToFloat(v)
}

// Join is joining input with separator
func Join(input []interface{}, sep string) string {
	var ret string
	for i, item := range input {
		if i > 0 {
			ret += sep
		}
		ret += fmt.Sprintf(`%v`, item)
	}
	return ret
}

// Split splits the input string to array
func Split(input, sep string) []interface{} {
	out := strings.Split(input, sep)
	result := make([]interface{}, len(out))
	for i, val := range out {
		result[i] = reflect.ValueOf(val).Interface()
	}
	return result
}

// Sha256 returns SHA256 hash value
func Sha256(text string) string {
	hash, err := crypto.Hash([]byte(text))
	if err != nil {
		log.WithFields(log.Fields{"value": text, "error": err, "type": consts.CryptoError}).Fatal("hashing text")
	}
	hash = converter.BinToHex(hash)
	return string(hash)
}

// PubToID returns a numeric identifier for the public key specified in the hexadecimal form.
func PubToID(hexkey string) int64 {
	pubkey, err := hex.DecodeString(hexkey)
	if err != nil {
		log.WithFields(log.Fields{"value": hexkey, "error": err, "type": consts.CryptoError}).Error("decoding hexkey to string")
		return 0
	}
	return crypto.Address(pubkey)
}

// HexToBytes converts the hexadecimal representation to []byte
func HexToBytes(hexdata string) ([]byte, error) {
	return hex.DecodeString(hexdata)
}

// LangRes returns the language resource
func LangRes(sc *SmartContract, appID int64, idRes, lang string) string {
	ret, _ := language.LangText(idRes, int(sc.TxSmart.EcosystemID), int(appID), lang, sc.VDE)
	return ret
}

// NewLang creates new language
func CreateLanguage(sc *SmartContract, name, trans string, appID int64) (id int64, err error) {
	if !accessContracts(sc, "NewLang", "NewLangJoint", "Import") {
		log.WithFields(log.Fields{"type": consts.IncorrectCallingContract}).Error("CreateLanguage can be only called from @1NewLang, @1NewLangJoint, @1Import")
		return 0, fmt.Errorf(`CreateLanguage can be only called from @1NewLang, @1NewLangJoint, @1Import`)
	}
	idStr := converter.Int64ToStr(sc.TxSmart.EcosystemID)
	if _, id, err = DBInsert(sc, `@`+idStr+"_languages", "name,res,app_id", name, trans, appID); err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("inserting new language")
		return 0, err
	}
	language.UpdateLang(int(sc.TxSmart.EcosystemID), int(appID), name, trans, sc.VDE)
	return id, nil
}

// EditLanguage edits language
func EditLanguage(sc *SmartContract, id int64, name, trans string, appID int64) error {
	if !accessContracts(sc, "EditLang", "EditLangJoint", "Import") {
		log.WithFields(log.Fields{"type": consts.IncorrectCallingContract}).Error("EditLanguage can be only called from @1EditLang, @1EditLangJoint and @1Import")
		return fmt.Errorf(`EditLanguage can be only called from @1EditLang, @1EditLangJoint and @1Import`)
	}
	idStr := converter.Int64ToStr(sc.TxSmart.EcosystemID)
	if _, err := DBUpdate(sc, `@`+idStr+"_languages", id, "name,res,app_id", name, trans, appID); err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("inserting new language")
		return err
	}
	language.UpdateLang(int(sc.TxSmart.EcosystemID), int(appID), name, trans, sc.VDE)
	return nil
}

// GetContractByName returns id of the contract with this name
func GetContractByName(sc *SmartContract, name string) int64 {
	contract := VMGetContract(sc.VM, name, uint32(sc.TxSmart.EcosystemID))
	if contract == nil {
		return 0
	}
	info := (*contract).Block.Info.(*script.ContractInfo)
	if info == nil {
		return 0
	}
	return info.Owner.TableID
}

// GetContractById returns the name of the contract with this id
func GetContractById(sc *SmartContract, id int64) string {
	_, ret, err := DBSelect(sc, "contracts", "value", id, `id`, 0, 1,
		0, ``, []interface{}{})
	if err != nil || len(ret) != 1 {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("getting contract name")
		return ``
	}

	re := regexp.MustCompile(`(?is)^\s*contract\s+([\d\w_]+)\s*{`)
	names := re.FindStringSubmatch(ret[0].(map[string]interface{})["value"].(string))
	if len(names) != 2 {
		return ``
	}
	return names[1]
}

// EvalCondition gets the condition and check it
func EvalCondition(sc *SmartContract, table, name, condfield string) error {
	conditions, err := model.Single(`SELECT `+converter.EscapeName(condfield)+` FROM "`+getDefTableName(sc, table)+
		`" WHERE name = ?`, name).String()
	if err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("executing single query")
		return err
	}
	if len(conditions) == 0 {
		log.WithFields(log.Fields{"type": consts.NotFound, "name": name}).Error("Record not found")
		return fmt.Errorf(`Record %s has not been found`, name)
	}
	return Eval(sc, conditions)
}

// Replace replaces old substrings to new substrings
func Replace(s, old, new string) string {
	return strings.Replace(s, old, new, -1)
}

// CreateEcosystem creates a new ecosystem
func CreateEcosystem(sc *SmartContract, wallet int64, name string) (int64, error) {
	if sc.TxContract.Name != `@1NewEcosystem` {
		log.WithFields(log.Fields{"type": consts.IncorrectCallingContract}).Error("CreateEcosystem can be only called from @1NewEcosystem")
		return 0, fmt.Errorf(`CreateEcosystem can be only called from @1NewEcosystem`)
	}

	var sp model.StateParameter
	sp.SetTablePrefix(`1`)
	found, err := sp.Get(sc.DbTransaction, `founder_account`)
	if err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("getting founder")
		return 0, err
	}

	if !found || len(sp.Value) == 0 {
		log.WithFields(log.Fields{"type": consts.NotFound, "error": ErrFounderAccount}).Error("founder not found")
		return 0, ErrFounderAccount
	}

	id, err := model.GetNextID(sc.DbTransaction, "1_ecosystems")
	if err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("generating next ecosystem id")
		return 0, err
	}

	if err = model.ExecSchemaEcosystem(sc.DbTransaction, int(id), wallet, name, converter.StrToInt64(sp.Value)); err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("executing ecosystem schema")
		return 0, err
	}

	idStr := converter.Int64ToStr(id)
	if err := LoadContract(sc.DbTransaction, idStr); err != nil {
		return 0, err
	}

	sc.Rollback = false
	sc.FullAccess = true
	if _, _, err = DBInsert(sc, `@`+idStr+"_pages", "id,name,value,menu,conditions", "1", "default_page",
		SysParamString("default_ecosystem_page"), "default_menu", `ContractConditions("MainCondition")`); err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("inserting default page")
		return 0, err
	}
	if _, _, err = DBInsert(sc, `@`+idStr+"_menu", "id,name,value,title,conditions", "1", "default_menu",
		SysParamString("default_ecosystem_menu"), "default", `ContractConditions("MainCondition")`); err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("inserting default page")
		return 0, err
	}

	var (
		ret []interface{}
		pub string
	)
	_, ret, err = DBSelect(sc, "@1_keys", "pub", wallet, `id`, 0, 1, 0, ``, []interface{}{})
	if err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("getting pub key")
		return 0, err
	}

	if Len(ret) > 0 {
		pub = ret[0].(map[string]interface{})[`pub`].(string)
	}
	if _, _, err := DBInsert(sc, `@`+idStr+"_keys", "id,pub", wallet, pub); err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("inserting default page")
		return 0, err
	}

	sc.FullAccess = false
	// because of we need to know which ecosystem to rollback.
	// All tables will be deleted so it's no need to rollback data from tables
	sc.Rollback = true
	if _, _, err := DBInsert(sc, "@1_ecosystems", "id,name", id, name); err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("insert new ecosystem to stat table")
		return 0, err
	}
	if !sc.VDE {
		if err := SysRollback(sc, `{"Type": "NewEcosystem"}`); err != nil {
			return 0, err
		}
	}
	return id, err
}

// EditEcosysName set newName for ecosystem
func EditEcosysName(sc *SmartContract, sysID int64, newName string) error {
	if sc.TxContract.Name != `@1EditEcosystemName` {
		log.WithFields(log.Fields{"type": consts.IncorrectCallingContract}).Error("EditEcosystemName can be only called from @1EditEcosystemName")
		return fmt.Errorf(`EditEcosystemName can be only called from @1EditEcosystemName`)
	}

	_, err := DBUpdate(sc, "@1_ecosystems", sysID, "name", newName)
	return err
}

// Size returns the length of the string
func Size(s string) int64 {
	return int64(len(s))
}

// Substr returns the substring of the string
func Substr(s string, off int64, slen int64) string {
	ilen := int64(len(s))
	if off < 0 || slen < 0 || off > ilen {
		return ``
	}
	if off+slen > ilen {
		return s[off:]
	}
	return s[off : off+slen]
}

// Activate sets Active status of the contract in smartVM
func Activate(sc *SmartContract, tblid int64, state int64) error {
	if !accessContracts(sc, nActivateContract, nDeactivateContract) {
		log.WithFields(log.Fields{"type": consts.IncorrectCallingContract}).Error("ActivateContract can be only called from @1ActivateContract or @1DeactivateContract")
		return fmt.Errorf(`ActivateContract can be only called from @1ActivateContract or @1DeactivateContract`)
	}
	ActivateContract(tblid, state, true)
	if !sc.VDE {
		if err := SysRollback(sc, fmt.Sprintf(`{"Type": "ActivateContract", "Id": "%d", "State": "%d"}`,
			tblid, state)); err != nil {
			return err
		}
	}
	return nil
}

// Deactivate sets Active status of the contract in smartVM
func Deactivate(sc *SmartContract, tblid int64, state int64) error {
	if !accessContracts(sc, nActivateContract, nDeactivateContract) {
		log.WithFields(log.Fields{"type": consts.IncorrectCallingContract}).Error("DeactivateContract can be only called from @1ActivateContract or @1DeactivateContract")
		return fmt.Errorf(`DeactivateContract can be only called from @1ActivateContract or @1DeactivateContract`)
	}
	ActivateContract(tblid, state, false)
	if !sc.VDE {
		if err := SysRollback(sc, fmt.Sprintf(`{"Type": "DeactivateContract", "Id": "%d", "State": "%d"}`,
			tblid, state)); err != nil {
			return err
		}
	}
	return nil
}

// CheckSignature checks the additional signatures for the contract
func CheckSignature(i *map[string]interface{}, name string) error {
	state, name := script.ParseContract(name)
	pref := converter.Int64ToStr(int64(state))
	sc := (*i)[`sc`].(*SmartContract)
	value, err := model.Single(`select value from "`+pref+`_signatures" where name=?`, name).String()
	if err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("executing single query")
		return err
	}
	if len(value) == 0 {
		return nil
	}
	hexsign, err := hex.DecodeString((*i)[`Signature`].(string))
	if len(hexsign) == 0 || err != nil {
		log.WithFields(log.Fields{"type": consts.ConversionError, "error": err}).Error("comverting signature to hex")
		return fmt.Errorf(`wrong signature`)
	}

	var sign TxSignJSON
	err = json.Unmarshal([]byte(value), &sign)
	if err != nil {
		log.WithFields(log.Fields{"type": consts.JSONUnmarshallError, "error": err}).Error("unmarshalling sign")
		return err
	}
	wallet := (*i)[`key_id`].(int64)
	forsign := fmt.Sprintf(`%d,%d`, uint64((*i)[`time`].(int64)), uint64(wallet))
	for _, isign := range sign.Params {
		val := (*i)[isign.Param]
		if val == nil {
			val = ``
		}
		forsign += fmt.Sprintf(`,%v`, val)
	}

	CheckSignResult, err := utils.CheckSign(sc.PublicKeys, forsign, hexsign, true)
	if err != nil {
		return err
	}
	if !CheckSignResult {
		log.WithFields(log.Fields{"type": consts.InvalidObject}).Error("incorrect signature")
		return fmt.Errorf(`incorrect signature ` + forsign)
	}
	return nil
}

// RollbackContract performs rollback for the contract
func RollbackContract(sc *SmartContract, name string) error {
	if !accessContracts(sc, nNewContract, nImport) {
		log.WithFields(log.Fields{"type": consts.IncorrectCallingContract, "error": errAccessRollbackContract}).Error("Check contract access")
		return errAccessRollbackContract
	}

	if c := VMGetContract(sc.VM, name, uint32(sc.TxSmart.EcosystemID)); c != nil {
		id := c.Block.Info.(*script.ContractInfo).ID
		if int(id) < len(sc.VM.Children) {
			sc.VM.Children = sc.VM.Children[:id]
		}
		delete(sc.VM.Objects, c.Name)
	}

	return nil
}

// DBSelectMetrics returns list of metrics by name and time interval
func DBSelectMetrics(sc *SmartContract, metric, timeInterval, aggregateFunc string) ([]interface{}, error) {
	result, err := model.GetMetricValues(metric, timeInterval, aggregateFunc)
	if err != nil {
		log.WithFields(log.Fields{"type": consts.DBError, "error": err}).Error("get values of metric")
		return nil, err
	}
	return result, nil
}

// DBCollectMetrics returns actual values of all metrics
// This function used to further store these values
func DBCollectMetrics() []interface{} {
	c := metric.NewCollector(
		metric.CollectMetricDataForEcosystemTables,
		metric.CollectMetricDataForEcosystemTx,
	)
	return c.Values()
}

// JSONDecode converts json string to object
func JSONDecode(input string) (interface{}, error) {
	var ret interface{}
	err := json.Unmarshal([]byte(input), &ret)
	if err != nil {
		log.WithFields(log.Fields{"type": consts.JSONUnmarshallError, "error": err}).Error("unmarshalling json")
		return nil, err
	}
	return ret, nil
}

// JSONEncode converts object to json string
func JSONEncode(input interface{}) (string, error) {
	rv := reflect.ValueOf(input)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	if rv.Kind() == reflect.Struct {
		return "", fmt.Errorf("Type %T doesn't support json marshalling", input)
	}

	b, err := json.Marshal(input)
	if err != nil {
		log.WithFields(log.Fields{"type": consts.JSONMarshallError, "error": err}).Error("marshalling json")
		return "", err
	}
	return string(b), nil
}

// Append syn for golang 'append' function
func Append(slice []interface{}, val interface{}) []interface{} {
	return append(slice, val)
}
