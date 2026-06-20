import { useSearchParams } from 'react-router-dom'
import { ContextActions } from './mongo-json/ContextActions'
import { ContextStrip } from './mongo-json/ContextStrip'
import { DiffMode } from './mongo-json/DiffMode'
import { EscapeMode } from './mongo-json/EscapeMode'
import { FormatMode } from './mongo-json/FormatMode'
import { ModeSwitch } from './mongo-json/ModeSwitch'
import { MongoJsonHero } from './mongo-json/MongoJsonHero'
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
    <div className="page-shell">
      <MongoJsonHero activeModeLabel={workspace.activeModeLabel} mode={mode} />

      <ModeSwitch mode={mode} onModeChange={setMode} />
      <ContextStrip
        actions={
          <ContextActions
            diffFocus={workspace.diffFocus}
            jumpToDiffPath={workspace.jumpToDiffPath}
            mode={mode}
            primaryDiffPath={workspace.primaryDiffPath}
            selectedRow={workspace.selectedRow}
            setDiffFocus={workspace.setDiffFocus}
            setSelectedRow={workspace.setSelectedRow}
            setShellFocus={workspace.setShellFocus}
            setTableQuery={workspace.setTableQuery}
            setTableTypeFilter={workspace.setTableTypeFilter}
            shellFocus={workspace.shellFocus}
            tableDataExists={Boolean(workspace.tableData)}
            tableQuery={workspace.tableQuery}
            tableTypeFilter={workspace.tableTypeFilter}
          />
        }
        trail={workspace.contextTrail}
      />

      {mode === 'format' ? (
        <FormatMode
          copied={workspace.copied}
          copyText={workspace.copyText}
          input={workspace.input}
          inputHint={workspace.inputHint}
          liveStatus={workspace.liveStatus}
          output={workspace.output}
          runFormat={workspace.runFormat}
          setInput={workspace.setInput}
          stats={workspace.stats}
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
