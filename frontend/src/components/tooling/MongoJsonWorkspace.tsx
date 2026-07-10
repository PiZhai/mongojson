import { useSearchParams } from 'react-router-dom'
import { DiffMode } from './mongo-json/DiffMode'
import { EscapeMode } from './mongo-json/EscapeMode'
import { FormatMode } from './mongo-json/FormatMode'
import { ModeSwitch } from './mongo-json/ModeSwitch'
import { RepairMode } from './mongo-json/RepairMode'
import { ShellMode } from './mongo-json/ShellMode'
import { TableMode } from './mongo-json/TableMode'
import { isMongoMode } from './mongo-json/modeMeta'
import type { MongoMode } from './mongo-json/types'
import { useMongoJsonWorkspaceState } from './mongo-json/useMongoJsonWorkspaceState'

export function MongoJsonWorkspace() {
  const [searchParams, setSearchParams] = useSearchParams()
  const rawMode = searchParams.get('mode')
  const mode: MongoMode = isMongoMode(rawMode) ? rawMode : 'format'
  const workspace = useMongoJsonWorkspaceState(mode)

  const setMode = (nextMode: MongoMode) => {
    const nextParams = new URLSearchParams(searchParams)
    nextParams.set('mode', nextMode)
    setSearchParams(nextParams, { replace: true })
  }

  return (
    <div className="page-shell mongo-json-page-shell layout-frame" data-layout-region="mongo-workspace">
      <ModeSwitch mode={mode} onModeChange={setMode} />
      {mode === 'format' ? (
        <FormatMode
          copied={workspace.copied}
          copyText={workspace.copyText}
          extendedJsonOutput={workspace.extendedJsonOutput}
          input={workspace.input}
          inputDiagnostics={workspace.inputDiagnostics}
          inputHint={workspace.inputHint}
          liveStatus={workspace.liveStatus}
          output={workspace.output}
          runFormat={workspace.runFormat}
          setInput={workspace.setInput}
          stats={workspace.stats}
          status={workspace.status}
        />
      ) : null}

      {mode === 'repair' ? (
        <RepairMode
          copied={workspace.copied}
          copyText={workspace.copyText}
          inputHint={workspace.inputHint}
          inputDiagnostics={workspace.inputDiagnostics}
          liveStatus={workspace.liveStatus}
          repairInput={workspace.repairInput}
          repairOutput={workspace.repairOutput}
          runRepair={workspace.runRepair}
          setRepairInput={workspace.setRepairInput}
          status={workspace.status}
        />
      ) : null}

      {mode === 'diff' ? (
        <DiffMode
          arrayMatchKey={workspace.arrayMatchKey}
          copied={workspace.copied}
          copyText={workspace.copyText}
          diffFocus={workspace.diffFocus}
          diffIgnoreInput={workspace.diffIgnoreInput}
          diffOverview={workspace.diffOverview}
          diffSummary={workspace.diffSummary}
          formattedJsonPatch={workspace.formattedJsonPatch}
          jumpToDiffPath={workspace.jumpToDiffPath}
          normalizedDiffLeft={workspace.normalizedDiffLeft}
          normalizedDiffRight={workspace.normalizedDiffRight}
          semanticDiff={workspace.semanticDiff}
          setArrayMatchKey={workspace.setArrayMatchKey}
          setDiffFocus={workspace.setDiffFocus}
          setDiffIgnoreInput={workspace.setDiffIgnoreInput}
          setDiffLeft={workspace.setDiffLeft}
          setDiffRight={workspace.setDiffRight}
        />
      ) : null}

      {mode === 'table' ? (
        <TableMode
          copied={workspace.copied}
          copyText={workspace.copyText}
          input={workspace.input}
          inputHint={workspace.inputHint}
          inputDiagnostics={workspace.inputDiagnostics}
          liveStatus={workspace.liveStatus}
          generatedSchema={workspace.generatedSchema}
          generatedSchemaTarget={workspace.generatedSchemaTarget}
          runTable={workspace.runTable}
          schemaProfile={workspace.schemaProfile}
          selectedRow={workspace.selectedRow}
          setGeneratedSchemaTarget={workspace.setGeneratedSchemaTarget}
          setInput={workspace.setInput}
          setSelectedRow={workspace.setSelectedRow}
          setTableQuery={workspace.setTableQuery}
          setTableTypeFilter={workspace.setTableTypeFilter}
          status={workspace.status}
          tableData={workspace.tableData}
          tableHasNoResults={workspace.tableHasNoResults}
          tableOverview={workspace.tableOverview}
          tablePreview={workspace.tablePreview}
          tableQuery={workspace.tableQuery}
          tableTypeFilter={workspace.tableTypeFilter}
        />
      ) : null}

      {mode === 'shell' ? (
        <ShellMode
          copied={workspace.copied}
          copyText={workspace.copyText}
          inputHint={workspace.inputHint}
          jumpToShellOffset={workspace.jumpToShellOffset}
          liveStatus={workspace.liveStatus}
          mongoInspection={workspace.mongoInspection}
          parsedShell={workspace.parsedShell}
          runShell={workspace.runShell}
          setShellFocus={workspace.setShellFocus}
          setShellInput={workspace.setShellInput}
          shellChecks={workspace.shellChecks}
          shellFocus={workspace.shellFocus}
          shellInput={workspace.shellInput}
          shellOutput={workspace.shellOutput}
          shellOverview={workspace.shellOverview}
          status={workspace.status}
        />
      ) : null}

      {mode === 'escape' || mode === 'unescape' ? (
        <EscapeMode
          copied={workspace.copied}
          copyText={workspace.copyText}
          escapeInput={workspace.escapeInput}
          escapeOutput={workspace.escapeOutput}
          inputHint={workspace.inputHint}
          inputDiagnostics={workspace.inputDiagnostics}
          liveStatus={workspace.liveStatus}
          mode={mode}
          runEscape={workspace.runEscape}
          setEscapeInput={workspace.setEscapeInput}
          status={workspace.status}
        />
      ) : null}
    </div>
  )
}
