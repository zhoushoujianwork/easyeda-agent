package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

type libraryAssetBuildSpec struct {
	Name, Description, LibraryUUID, Designator string
	Symbol                                     struct {
		Name, Description string
		Geometry          map[string]any
	}
	Footprint struct {
		Name, Description string
		Geometry          map[string]any
	}
	Model3D    *struct{ Name, Description, File, Unit string } `json:"model3D"`
	Properties map[string]any
}

func resultString(res *actionResult, key string) string {
	if res == nil {
		return ""
	}
	v, _ := res.Result[key].(string)
	return v
}

// newLibCmd returns the "lib" subcommand group.
func newLibCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string

	lib := &cobra.Command{
		Use:   "lib",
		Short: "EasyEDA device library operations",
	}
	lib.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")

	// ── lib libraries ─────────────────────────────────────────────────────
	lib.AddCommand(&cobra.Command{
		Use:   "libraries",
		Short: "List EasyEDA libraries and writable personal/project library UUIDs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return dispatch(cfg, "library.list", window, map[string]any{}, stdout, stderr)
		},
	})

	// ── lib search ────────────────────────────────────────────────────────
	// schematic.library.search
	{
		var query, libraryUUID string
		var limit int
		var exact bool
		var allowFuzzy bool
		c := &cobra.Command{
			Use:   "search",
			Short: "Search the EasyEDA device library by MPN, value+package, or name",
			Args:  cobra.NoArgs,
			Example: `  easyeda lib search --query "ESP32-S3-WROOM-1"
  easyeda lib search --query "100nF 0402" --limit 5
  easyeda lib search --query C5665              # exact LCSC match (auto-detected)
  easyeda lib search --query C5665 --allow-fuzzy # keep the fuzzy ranked results`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if query == "" {
					return fmt.Errorf("--query is required")
				}
				if exact && allowFuzzy {
					return fmt.Errorf("--exact and --allow-fuzzy are mutually exclusive")
				}
				payload := map[string]any{"query": query}
				if libraryUUID != "" {
					payload["libraryUuid"] = libraryUUID
				}
				if cmd.Flags().Changed("limit") {
					payload["limit"] = limit
				}
				// --allow-fuzzy forces the ranked free-text path even for a bare C-number.
				// A bare C-number query is exact by default (the connector auto-detects
				// ^C\d+$); --exact is accepted for explicitness but changes nothing on its own.
				if allowFuzzy {
					payload["allowFuzzy"] = true
				}
				return dispatch(cfg, "schematic.library.search", window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&query, "query", "", "search query: MPN, value+package, or component name (required)")
		c.Flags().StringVar(&libraryUUID, "library", "", "limit search to one library UUID")
		c.Flags().IntVar(&limit, "limit", 10, "maximum number of results to return")
		c.Flags().BoolVar(&exact, "exact", false, "when --query is an LCSC C-number, only return devices whose LCSC field matches exactly (default for C-numbers)")
		c.Flags().BoolVar(&allowFuzzy, "allow-fuzzy", false, "keep the fuzzy ranked results even when --query is an LCSC C-number")
		lib.AddCommand(c)
	}

	// ── lib by-lcsc ─────────────────────────────────────────────────────────
	// schematic.library.get_by_lcsc — deterministic resolve of LCSC C-numbers to
	// { libraryUuid, uuid } ready for schematic.component.place (the standard-
	// parts.json / BOM path; no free-text ranking).
	{
		var lcsc []string
		c := &cobra.Command{
			Use:   "by-lcsc",
			Short: "Resolve LCSC C-numbers directly to device-library identity (libraryUuid + uuid)",
			Args:  cobra.NoArgs,
			Example: `  easyeda lib by-lcsc --lcsc C6186
  easyeda lib by-lcsc --lcsc C6186 --lcsc C9900163599
  easyeda lib by-lcsc --lcsc C6186,C9900163599`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if len(lcsc) == 0 {
					return fmt.Errorf("--lcsc is required (one or more LCSC C-numbers)")
				}
				return dispatch(cfg, "schematic.library.get_by_lcsc", window,
					map[string]any{"lcscIds": lcsc}, stdout, stderr)
			},
		}
		c.Flags().StringSliceVar(&lcsc, "lcsc", nil, "LCSC C-number(s); repeat the flag or comma-separate (required)")
		lib.AddCommand(c)
	}

	lib.AddCommand(newLibraryFootprintCmd(cfg, stdout, stderr, &window))
	lib.AddCommand(newLibrarySymbolCmd(cfg, stdout, stderr, &window))
	lib.AddCommand(newLibraryModel3DCmd(cfg, stdout, stderr, &window))
	lib.AddCommand(newLibraryDeviceCmd(cfg, stdout, stderr, &window))

	return lib
}

func libraryTargetPayload(scope, libraryUUID string, classification []string) map[string]any {
	payload := map[string]any{}
	if libraryUUID != "" {
		payload["libraryUuid"] = libraryUUID
	} else if scope != "" {
		payload["scope"] = scope
	}
	if len(classification) > 0 {
		payload["classification"] = classification
	}
	return payload
}

func addLibraryTargetFlags(c *cobra.Command, scope, libraryUUID *string, classification *[]string) {
	c.Flags().StringVar(scope, "scope", "personal", "target writable library: personal or project")
	c.Flags().StringVar(libraryUUID, "library", "", "explicit target library UUID (overrides --scope)")
	c.Flags().StringSliceVar(classification, "classification", nil, "library classification path/index strings")
}

func addLibraryDeleteCmd(group *cobra.Command, cfg *appConfig, stdout, stderr io.Writer, window *string, action, noun string) {
	var uuid, libraryUUID, expectedName string
	c := &cobra.Command{
		Use: "delete", Short: "Delete one exact " + noun + " and verify absence", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if uuid == "" || libraryUUID == "" || expectedName == "" {
				return fmt.Errorf("--uuid, --library and --expected-name are required")
			}
			return dispatch(cfg, action, *window, map[string]any{
				"uuid": uuid, "libraryUuid": libraryUUID, "expectedName": expectedName,
			}, stdout, stderr)
		},
	}
	c.Flags().StringVar(&uuid, "uuid", "", noun+" UUID (required)")
	c.Flags().StringVar(&libraryUUID, "library", "", "library UUID (required)")
	c.Flags().StringVar(&expectedName, "expected-name", "", "exact current name safety check (required)")
	group.AddCommand(c)
}

func newLibraryFootprintCmd(cfg *appConfig, stdout, stderr io.Writer, window *string) *cobra.Command {
	group := &cobra.Command{Use: "footprint", Short: "Create and inspect footprint library assets"}
	{
		var name, description, scope, libraryUUID string
		var classification []string
		c := &cobra.Command{
			Use:   "create",
			Short: "Create an empty footprint asset and verify it by readback",
			Args:  cobra.NoArgs,
			Example: `  easyeda lib footprint create --name MY_SOT23
  easyeda lib footprint create --name MY_SOT23 --scope project`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if name == "" {
					return fmt.Errorf("--name is required")
				}
				payload := libraryTargetPayload(scope, libraryUUID, classification)
				payload["name"] = name
				if description != "" {
					payload["description"] = description
				}
				return dispatch(cfg, "library.footprint.create", *window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&name, "name", "", "footprint name (required)")
		c.Flags().StringVar(&description, "description", "", "footprint description")
		addLibraryTargetFlags(c, &scope, &libraryUUID, &classification)
		group.AddCommand(c)
	}
	addLibraryDeleteCmd(group, cfg, stdout, stderr, window, "library.footprint.delete", "footprint")
	{
		var uuid, libraryUUID string
		c := &cobra.Command{
			Use: "get", Short: "Read a footprint asset", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if uuid == "" {
					return fmt.Errorf("--uuid is required")
				}
				payload := map[string]any{"uuid": uuid}
				if libraryUUID != "" {
					payload["libraryUuid"] = libraryUUID
				}
				return dispatch(cfg, "library.footprint.get", *window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&uuid, "uuid", "", "footprint UUID (required)")
		c.Flags().StringVar(&libraryUUID, "library", "", "library UUID")
		group.AddCommand(c)
	}
	{
		var uuid, sourceLibrary, name, scope, libraryUUID string
		var classification []string
		c := &cobra.Command{
			Use: "copy", Short: "Losslessly copy an existing footprint into a writable library", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if uuid == "" || sourceLibrary == "" || name == "" {
					return fmt.Errorf("--uuid, --source-library and --name are required")
				}
				payload := libraryTargetPayload(scope, libraryUUID, classification)
				payload["uuid"] = uuid
				payload["sourceLibraryUuid"] = sourceLibrary
				payload["name"] = name
				return dispatch(cfg, "library.footprint.copy", *window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&uuid, "uuid", "", "source footprint UUID (required)")
		c.Flags().StringVar(&sourceLibrary, "source-library", "", "source library UUID (required)")
		c.Flags().StringVar(&name, "name", "", "new name before EA_AGENT namespace (required)")
		addLibraryTargetFlags(c, &scope, &libraryUUID, &classification)
		group.AddCommand(c)
	}
	{
		var uuid, libraryUUID, specPath string
		c := &cobra.Command{
			Use:     "build",
			Short:   "Author pad/line geometry in an existing footprint from a JSON spec",
			Args:    cobra.NoArgs,
			Example: `  easyeda lib footprint build --uuid <fp> --library <lib> --spec footprint.json`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if uuid == "" || libraryUUID == "" || specPath == "" {
					return fmt.Errorf("--uuid, --library and --spec are required")
				}
				data, err := os.ReadFile(specPath)
				if err != nil {
					return fmt.Errorf("read --spec: %w", err)
				}
				var spec map[string]any
				if err := json.Unmarshal(data, &spec); err != nil {
					return fmt.Errorf("parse --spec: %w", err)
				}
				spec["uuid"] = uuid
				spec["libraryUuid"] = libraryUUID
				return dispatch(cfg, "library.footprint.build", *window, spec, stdout, stderr)
			},
		}
		c.Flags().StringVar(&uuid, "uuid", "", "footprint UUID returned by footprint create (required)")
		c.Flags().StringVar(&libraryUUID, "library", "", "footprint library UUID (required)")
		c.Flags().StringVar(&specPath, "spec", "", "JSON file containing pads[] and/or lines[] (required)")
		group.AddCommand(c)
	}
	return group
}

func newLibrarySymbolCmd(cfg *appConfig, stdout, stderr io.Writer, window *string) *cobra.Command {
	group := &cobra.Command{Use: "symbol", Short: "Create and inspect symbol library assets"}
	{
		var name, description, scope, libraryUUID string
		var classification []string
		var symbolType int
		c := &cobra.Command{
			Use: "create", Short: "Create an empty symbol asset and verify it by readback", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if name == "" {
					return fmt.Errorf("--name is required")
				}
				payload := libraryTargetPayload(scope, libraryUUID, classification)
				payload["name"] = name
				if description != "" {
					payload["description"] = description
				}
				if cmd.Flags().Changed("symbol-type") {
					payload["symbolType"] = symbolType
				}
				return dispatch(cfg, "library.symbol.create", *window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&name, "name", "", "symbol name (required)")
		c.Flags().StringVar(&description, "description", "", "symbol description")
		c.Flags().IntVar(&symbolType, "symbol-type", 0, "official ELIB_SymbolType numeric value")
		addLibraryTargetFlags(c, &scope, &libraryUUID, &classification)
		group.AddCommand(c)
	}
	{
		var uuid, libraryUUID, specPath string
		c := &cobra.Command{Use: "build", Short: "Author symbol outline and pins from a JSON spec", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if uuid == "" || libraryUUID == "" || specPath == "" {
				return fmt.Errorf("--uuid, --library and --spec are required")
			}
			data, err := os.ReadFile(specPath)
			if err != nil {
				return fmt.Errorf("read --spec: %w", err)
			}
			var spec map[string]any
			if err := json.Unmarshal(data, &spec); err != nil {
				return fmt.Errorf("parse --spec: %w", err)
			}
			spec["uuid"], spec["libraryUuid"] = uuid, libraryUUID
			return dispatch(cfg, "library.symbol.build", *window, spec, stdout, stderr)
		}}
		c.Flags().StringVar(&uuid, "uuid", "", "symbol UUID returned by symbol create (required)")
		c.Flags().StringVar(&libraryUUID, "library", "", "symbol library UUID (required)")
		c.Flags().StringVar(&specPath, "spec", "", "JSON file containing outline[] and pins[] (required)")
		group.AddCommand(c)
	}
	{
		var uuid, libraryUUID string
		c := &cobra.Command{
			Use: "get", Short: "Read a symbol asset", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if uuid == "" {
					return fmt.Errorf("--uuid is required")
				}
				payload := map[string]any{"uuid": uuid}
				if libraryUUID != "" {
					payload["libraryUuid"] = libraryUUID
				}
				return dispatch(cfg, "library.symbol.get", *window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&uuid, "uuid", "", "symbol UUID (required)")
		c.Flags().StringVar(&libraryUUID, "library", "", "library UUID")
		group.AddCommand(c)
	}
	addLibraryDeleteCmd(group, cfg, stdout, stderr, window, "library.symbol.delete", "symbol")
	return group
}

func newLibraryModel3DCmd(cfg *appConfig, stdout, stderr io.Writer, window *string) *cobra.Command {
	group := &cobra.Command{Use: "model3d", Short: "Import and inspect 3D model library assets"}
	{
		var name, file, unit, description, scope, libraryUUID string
		var classification []string
		c := &cobra.Command{Use: "create", Short: "Import a STEP/3D model file and verify it by readback", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" || file == "" {
				return fmt.Errorf("--name and --file are required")
			}
			data, err := os.ReadFile(file)
			if err != nil {
				return fmt.Errorf("read --file: %w", err)
			}
			payload := libraryTargetPayload(scope, libraryUUID, classification)
			payload["name"], payload["fileName"], payload["dataBase64"], payload["unit"] = name, filepath.Base(file), base64.StdEncoding.EncodeToString(data), unit
			if description != "" {
				payload["description"] = description
			}
			return dispatch(cfg, "library.model3d.create", *window, payload, stdout, stderr)
		}}
		c.Flags().StringVar(&name, "name", "", "3D model name before EA_AGENT namespace (required)")
		c.Flags().StringVar(&file, "file", "", "STEP/3D model file (required)")
		c.Flags().StringVar(&unit, "unit", "mm", "source model unit: mm, cm, m, mil, or inch")
		c.Flags().StringVar(&description, "description", "", "3D model description")
		addLibraryTargetFlags(c, &scope, &libraryUUID, &classification)
		group.AddCommand(c)
	}
	{
		var uuid, libraryUUID string
		c := &cobra.Command{Use: "get", Short: "Read a 3D model asset", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if uuid == "" {
				return fmt.Errorf("--uuid is required")
			}
			p := map[string]any{"uuid": uuid}
			if libraryUUID != "" {
				p["libraryUuid"] = libraryUUID
			}
			return dispatch(cfg, "library.model3d.get", *window, p, stdout, stderr)
		}}
		c.Flags().StringVar(&uuid, "uuid", "", "3D model UUID (required)")
		c.Flags().StringVar(&libraryUUID, "library", "", "library UUID")
		group.AddCommand(c)
	}
	addLibraryDeleteCmd(group, cfg, stdout, stderr, window, "library.model3d.delete", "3D model")
	{
		var query, libraryUUID string
		var classification []string
		var limit int
		c := &cobra.Command{Use: "search", Short: "Search existing 3D models", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if query == "" { return fmt.Errorf("--query is required") }
			p := map[string]any{"query": query, "limit": limit}
			if libraryUUID != "" { p["libraryUuid"] = libraryUUID }
			if len(classification) > 0 { p["classification"] = classification }
			return dispatch(cfg, "library.model3d.search", *window, p, stdout, stderr)
		}}
		c.Flags().StringVar(&query, "query", "", "model name keyword (required)")
		c.Flags().StringVar(&libraryUUID, "library", "", "limit search to one library UUID")
		c.Flags().StringSliceVar(&classification, "classification", nil, "library classification path/index strings")
		c.Flags().IntVar(&limit, "limit", 20, "maximum results, 1..100")
		group.AddCommand(c)
	}
	{
		var uuid, sourceLibrary, name, scope, libraryUUID string
		var classification []string
		c := &cobra.Command{Use: "copy", Short: "Copy an existing 3D model into a writable library", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if uuid == "" || sourceLibrary == "" || name == "" { return fmt.Errorf("--uuid, --source-library and --name are required") }
			p := libraryTargetPayload(scope, libraryUUID, classification)
			p["uuid"], p["sourceLibraryUuid"], p["name"] = uuid, sourceLibrary, name
			return dispatch(cfg, "library.model3d.copy", *window, p, stdout, stderr)
		}}
		c.Flags().StringVar(&uuid, "uuid", "", "source 3D model UUID (required)")
		c.Flags().StringVar(&sourceLibrary, "source-library", "", "source library UUID (required)")
		c.Flags().StringVar(&name, "name", "", "new name before EA_AGENT namespace (required)")
		addLibraryTargetFlags(c, &scope, &libraryUUID, &classification)
		group.AddCommand(c)
	}
	return group
}

func newLibraryDeviceCmd(cfg *appConfig, stdout, stderr io.Writer, window *string) *cobra.Command {
	group := &cobra.Command{Use: "device", Short: "Create and inspect Device records that bind symbols and footprints"}
	{
		var name, description, scope, libraryUUID string
		var symbolUUID, symbolLibrary, footprintUUID, footprintLibrary, modelUUID, modelLibrary string
		var designator, manufacturer, manufacturerID, supplier, supplierID, propertiesJSON string
		var classification []string
		var addBOM, addPCB bool
		c := &cobra.Command{
			Use: "create", Short: "Create a Device bound to an existing symbol and optional footprint", Args: cobra.NoArgs,
			Example: `  easyeda lib device create --name MY_PART \
    --symbol-uuid <uuid> --symbol-library <lib> \
    --footprint-uuid <uuid> --footprint-library <lib> --designator U`,
			RunE: func(cmd *cobra.Command, args []string) error {
				if name == "" {
					return fmt.Errorf("--name is required")
				}
				if symbolUUID == "" || symbolLibrary == "" {
					return fmt.Errorf("--symbol-uuid and --symbol-library are required")
				}
				if (footprintUUID == "") != (footprintLibrary == "") {
					return fmt.Errorf("--footprint-uuid and --footprint-library must be provided together")
				}
				if (modelUUID == "") != (modelLibrary == "") {
					return fmt.Errorf("--model3d-uuid and --model3d-library must be provided together")
				}
				payload := libraryTargetPayload(scope, libraryUUID, classification)
				payload["name"] = name
				payload["symbol"] = map[string]any{"uuid": symbolUUID, "libraryUuid": symbolLibrary}
				if footprintUUID != "" {
					payload["footprint"] = map[string]any{"uuid": footprintUUID, "libraryUuid": footprintLibrary}
				}
				if modelUUID != "" {
					payload["model3D"] = map[string]any{"uuid": modelUUID, "libraryUuid": modelLibrary}
				}
				if description != "" {
					payload["description"] = description
				}
				property := map[string]any{
					"addIntoBom": addBOM,
					"addIntoPcb": addPCB,
				}
				if propertiesJSON != "" {
					otherProperty := map[string]any{}
					if err := json.Unmarshal([]byte(propertiesJSON), &otherProperty); err != nil {
						return fmt.Errorf("--properties: %w", err)
					}
					property["otherProperty"] = otherProperty
				}
				if designator != "" {
					property["designator"] = designator
				}
				if manufacturer != "" {
					property["manufacturer"] = manufacturer
				}
				if manufacturerID != "" {
					property["manufacturerId"] = manufacturerID
				}
				if supplier != "" {
					property["supplier"] = supplier
				}
				if supplierID != "" {
					property["supplierId"] = supplierID
				}
				payload["property"] = property
				return dispatch(cfg, "library.device.create", *window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&name, "name", "", "device name (required)")
		c.Flags().StringVar(&description, "description", "", "device description")
		c.Flags().StringVar(&symbolUUID, "symbol-uuid", "", "symbol UUID (required)")
		c.Flags().StringVar(&symbolLibrary, "symbol-library", "", "symbol library UUID (required)")
		c.Flags().StringVar(&footprintUUID, "footprint-uuid", "", "footprint UUID")
		c.Flags().StringVar(&footprintLibrary, "footprint-library", "", "footprint library UUID")
		c.Flags().StringVar(&modelUUID, "model3d-uuid", "", "3D model UUID")
		c.Flags().StringVar(&modelLibrary, "model3d-library", "", "3D model library UUID")
		c.Flags().StringVar(&designator, "designator", "", "default designator prefix, e.g. U/R/C")
		c.Flags().StringVar(&manufacturer, "manufacturer", "", "manufacturer")
		c.Flags().StringVar(&manufacturerID, "mpn", "", "manufacturer part number")
		c.Flags().StringVar(&supplier, "supplier", "", "supplier")
		c.Flags().StringVar(&supplierID, "supplier-id", "", "supplier/LCSC part number")
		c.Flags().StringVar(&propertiesJSON, "properties", "", "additional Device otherProperty object as JSON")
		c.Flags().BoolVar(&addBOM, "bom", true, "include in BOM")
		c.Flags().BoolVar(&addPCB, "pcb", true, "transfer to PCB")
		addLibraryTargetFlags(c, &scope, &libraryUUID, &classification)
		group.AddCommand(c)
	}
	{
		var specPath string
		c := &cobra.Command{Use: "build", Short: "Create Symbol + Footprint + optional 3D model and bind one Device", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if specPath == "" {
				return fmt.Errorf("--spec is required")
			}
			data, err := os.ReadFile(specPath)
			if err != nil {
				return fmt.Errorf("read --spec: %w", err)
			}
			var spec libraryAssetBuildSpec
			if err := json.Unmarshal(data, &spec); err != nil {
				return fmt.Errorf("parse --spec: %w", err)
			}
			if spec.Name == "" || spec.LibraryUUID == "" || spec.Symbol.Name == "" || spec.Footprint.Name == "" || len(spec.Symbol.Geometry) == 0 || len(spec.Footprint.Geometry) == 0 {
				return fmt.Errorf("spec requires name, libraryUUID, symbol{name,geometry}, and footprint{name,geometry}")
			}
			var log bytes.Buffer
			var symbolUUID, symbolName, footprintUUID, footprintName, modelUUID, modelName string
			rollback := func() {
				if modelUUID != "" {
					_, _ = dispatchCapture(cfg, "library.model3d.delete", *window, map[string]any{"uuid": modelUUID, "libraryUuid": spec.LibraryUUID, "expectedName": modelName}, &log)
				}
				if footprintUUID != "" {
					_, _ = dispatchCapture(cfg, "library.footprint.delete", *window, map[string]any{"uuid": footprintUUID, "libraryUuid": spec.LibraryUUID, "expectedName": footprintName}, &log)
				}
				if symbolUUID != "" {
					_, _ = dispatchCapture(cfg, "library.symbol.delete", *window, map[string]any{"uuid": symbolUUID, "libraryUuid": spec.LibraryUUID, "expectedName": symbolName}, &log)
				}
			}
			fail := func(stage string, cause error) error {
				rollback()
				fmt.Fprintf(stderr, "device build failed at %s; rollback attempted\n", stage)
				return cause
			}
			target := map[string]any{"libraryUuid": spec.LibraryUUID}
			sp := map[string]any{"name": spec.Symbol.Name, "libraryUuid": spec.LibraryUUID}
			if spec.Symbol.Description != "" {
				sp["description"] = spec.Symbol.Description
			}
			sr, err := dispatchCapture(cfg, "library.symbol.create", *window, sp, &log)
			if err != nil {
				return fail("symbol.create", err)
			}
			symbolUUID, symbolName = resultString(sr, "uuid"), resultString(sr, "name")
			sg := spec.Symbol.Geometry
			sg["uuid"], sg["libraryUuid"] = symbolUUID, spec.LibraryUUID
			if _, err = dispatchCapture(cfg, "library.symbol.build", *window, sg, &log); err != nil {
				return fail("symbol.build", err)
			}
			fp := map[string]any{"name": spec.Footprint.Name, "libraryUuid": spec.LibraryUUID}
			if spec.Footprint.Description != "" {
				fp["description"] = spec.Footprint.Description
			}
			fr, err := dispatchCapture(cfg, "library.footprint.create", *window, fp, &log)
			if err != nil {
				return fail("footprint.create", err)
			}
			footprintUUID, footprintName = resultString(fr, "uuid"), resultString(fr, "name")
			fg := spec.Footprint.Geometry
			fg["uuid"], fg["libraryUuid"] = footprintUUID, spec.LibraryUUID
			if _, err = dispatchCapture(cfg, "library.footprint.build", *window, fg, &log); err != nil {
				return fail("footprint.build", err)
			}
			if spec.Model3D != nil {
				md, readErr := os.ReadFile(spec.Model3D.File)
				if readErr != nil {
					return fail("model3d.file", readErr)
				}
				mp := map[string]any{"name": spec.Model3D.Name, "libraryUuid": spec.LibraryUUID, "fileName": filepath.Base(spec.Model3D.File), "dataBase64": base64.StdEncoding.EncodeToString(md), "unit": spec.Model3D.Unit}
				if spec.Model3D.Description != "" {
					mp["description"] = spec.Model3D.Description
				}
				if spec.Model3D.Unit == "" {
					mp["unit"] = "mm"
				}
				mr, createErr := dispatchCapture(cfg, "library.model3d.create", *window, mp, &log)
				if createErr != nil {
					return fail("model3d.create", createErr)
				}
				modelUUID, modelName = resultString(mr, "uuid"), resultString(mr, "name")
			}
			dp := target
			dp["name"], dp["symbol"], dp["footprint"] = spec.Name, map[string]any{"uuid": symbolUUID, "libraryUuid": spec.LibraryUUID}, map[string]any{"uuid": footprintUUID, "libraryUuid": spec.LibraryUUID}
			if spec.Description != "" {
				dp["description"] = spec.Description
			}
			if modelUUID != "" {
				dp["model3D"] = map[string]any{"uuid": modelUUID, "libraryUuid": spec.LibraryUUID}
			}
			prop := map[string]any{"addIntoBom": true, "addIntoPcb": true, "designator": spec.Designator, "otherProperty": spec.Properties}
			dp["property"] = prop
			dr, err := dispatchCapture(cfg, "library.device.create", *window, dp, &log)
			if err != nil {
				return fail("device.create", err)
			}
			return json.NewEncoder(stdout).Encode(map[string]any{"ok": true, "result": map[string]any{"device": map[string]any{"uuid": resultString(dr, "uuid"), "name": resultString(dr, "name")}, "symbol": map[string]any{"uuid": symbolUUID, "name": symbolName}, "footprint": map[string]any{"uuid": footprintUUID, "name": footprintName}, "model3D": func() any {
				if modelUUID == "" {
					return nil
				}
				return map[string]any{"uuid": modelUUID, "name": modelName}
			}(), "verified": true}})
		}}
		c.Flags().StringVar(&specPath, "spec", "", "JSON file describing the complete Device asset set (required)")
		group.AddCommand(c)
	}
	{
		var uuid, libraryUUID string
		c := &cobra.Command{
			Use: "get", Short: "Read a Device and its symbol/footprint association", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				if uuid == "" {
					return fmt.Errorf("--uuid is required")
				}
				payload := map[string]any{"uuid": uuid}
				if libraryUUID != "" {
					payload["libraryUuid"] = libraryUUID
				}
				return dispatch(cfg, "library.device.get", *window, payload, stdout, stderr)
			},
		}
		c.Flags().StringVar(&uuid, "uuid", "", "Device UUID (required)")
		c.Flags().StringVar(&libraryUUID, "library", "", "library UUID")
		group.AddCommand(c)
	}
	addLibraryDeleteCmd(group, cfg, stdout, stderr, window, "library.device.delete", "Device")
	{
		var uuid, libraryUUID, expectedName, modelUUID, modelLibrary string
		var clear bool
		c := &cobra.Command{Use: "model3d", Short: "Bind, replace, or clear the 3D model on an existing Device", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			if uuid == "" || libraryUUID == "" || expectedName == "" { return fmt.Errorf("--uuid, --library and --expected-name are required") }
			if clear && (modelUUID != "" || modelLibrary != "") { return fmt.Errorf("--clear cannot be combined with model flags") }
			if !clear && (modelUUID == "" || modelLibrary == "") { return fmt.Errorf("--model3d-uuid and --model3d-library are required unless --clear is used") }
			p := map[string]any{"uuid": uuid, "libraryUuid": libraryUUID, "expectedName": expectedName, "clear": clear}
			if !clear { p["model3D"] = map[string]any{"uuid": modelUUID, "libraryUuid": modelLibrary} }
			return dispatch(cfg, "library.device.set_model3d", *window, p, stdout, stderr)
		}}
		c.Flags().StringVar(&uuid, "uuid", "", "Device UUID (required)")
		c.Flags().StringVar(&libraryUUID, "library", "", "Device library UUID (required)")
		c.Flags().StringVar(&expectedName, "expected-name", "", "exact Device name safety check (required)")
		c.Flags().StringVar(&modelUUID, "model3d-uuid", "", "3D model UUID")
		c.Flags().StringVar(&modelLibrary, "model3d-library", "", "3D model library UUID")
		c.Flags().BoolVar(&clear, "clear", false, "remove the current 3D model association")
		group.AddCommand(c)
	}
	return group
}
