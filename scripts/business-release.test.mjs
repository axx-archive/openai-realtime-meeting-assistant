import assert from 'node:assert/strict'
import test from 'node:test'
import { createHash } from 'node:crypto'
import { mkdtemp, mkdir, writeFile, readFile, chmod, rm, unlink, symlink } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import {
  validateReleaseTransactionJournal, releaseTransactionCompletionEvidence,
  acquireReleaseOperationLock, businessDatabaseValue, businessDatabaseEnvPatch,
  businessDatabaseEnvState, requestedTargetBaseEnvPatch, nextBusinessEnvironment,
  assertBusinessEnvironmentBinding, prepareTargetBaseEnvPatch, installTargetBaseEnvPatch,
  assertBusinessHostBinding, verifyBusinessPrivateEnvironment,
  executeDurableReleasePhaseMachine, executeDurableReleaseRecoveryPhaseMachine,
  restorePriorBaseEnv, commitPriorBaseEnvRestore, commitTargetBaseEnvPatch,
  reinstallCommittedTargetBaseEnvPatch, validateBaseEnvPatchReceipt, baseEnvPatchPlanFromReceipt,
  prepareQualificationRollbackBaseEnvPatch, verifyBaseEnvPatchRuntimeEnvironment
} from './bonfire-release.mjs'
const key = 'STRIDE_BUSINESS_DATABASE_URL'
const value = 'postgres://stride_business_app:0123456789abcdef@canonical-postgres:5432/stride_business?sslmode=disable'
const hash = x => createHash('sha256').update(x).digest('hex')
const uid = process.getuid()
async function fixture(t, before = Buffer.from('SECRET=unchanged\r\nPRIVATE_REALTIME_VOICE_QUALIFIED=false')) {
  const root = await mkdtemp(join(tmpdir(), 'business-release-'))
  const backupRoot = join(root, 'backups')
  await chmod(root, 0o700); await mkdir(backupRoot, { mode: 0o700 })
  const baseEnv = join(root, '.env'), valueFile = join(root, 'value')
  await writeFile(baseEnv, before, { mode: 0o600 }); await writeFile(valueFile, value, { mode: 0o600 })
  const lock = await acquireReleaseOperationLock(join(root, 'target'), join(root, 'prior'))
  t.after(async () => { await lock.release().catch(() => {}); await rm(root, { recursive: true, force: true }) })
  const args = { baseEnv, request: { patchKey: key, valueFile, backupDir: backupRoot, expectedBeforeSha256: hash(before) }, operationLock: lock, targetReleaseCommit: 'a'.repeat(40), rollbackReleaseCommit: 'b'.repeat(40), targetLedgerGeneration: 2, ownerUid: uid, backupRoot }
  const plan = await prepareTargetBaseEnvPatch(args)
  return { root, backupRoot, baseEnv, valueFile, before, plan, lock, args }
}

test('Business DSN is narrowly typed and dotenv cannot interpret its bytes', () => {
  assert.equal(businessDatabaseValue(value), value)
  for (const bad of [value+'\n', value.replace('stride_business_app','bonfire'), value.replace('canonical-postgres','localhost'), value.replace('/stride_business?','/bonfire?'), value.replace('0123456789abcdef','$SECRET'), value+'#fragment', '"'+value+'"']) assert.throws(() => businessDatabaseValue(bad))
  for (const before of [`${key}=`, `# ${key} remains absent`, `export ${key}=x`]) assert.throws(() => businessDatabaseEnvPatch(before, value))
  const before = Buffer.from('OTHER=keep\r\nLAST=exact')
  const patch = businessDatabaseEnvPatch(before, value)
  assert.deepEqual(patch.after.subarray(0,before.length), before)
  assert.equal(businessDatabaseEnvState(patch.after), hash(value))
  assert.equal(businessDatabaseEnvState(before), null)
})

test('only explicit Business file activation accepts these options', () => {
  const options = { businessDatabaseValueFile: '/root/private-value', targetBaseEnvExpectedSha256: 'a'.repeat(64), targetBaseEnvBackupDir: '/opt/meetingassist-backups' }
  assert.equal(requestedTargetBaseEnvPatch(options,'activated').patchKey, key)
  assert.throws(() => requestedTargetBaseEnvPatch(options,'rolledBack'))
  assert.throws(() => requestedTargetBaseEnvPatch({...options,targetBaseEnvPatchValue:value},'activated'))
  assert.throws(() => requestedTargetBaseEnvPatch({...options,businessDatabaseValueFile:'relative'},'activated'))
  assert.throws(() => requestedTargetBaseEnvPatch({...options,targetBaseEnvExpectedSha256:'wrong'},'activated'))
})

test('private retained value permits restart without original file; redacted receipts reverse exactly', async t => {
  const f = await fixture(t)
  await unlink(f.valueFile)
  assert.deepEqual(await readFile(f.baseEnv), f.before)
  assert.equal((await readFile(f.plan.valuePath)).toString(), value)
  const installed = await installTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot})
  assert.equal(installed.state,'target_installed')
  assert.deepEqual(await readFile(f.plan.backupPath),f.before)
  await installTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot})
  const receipt = await commitTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot})
  assert.equal(receipt.state,'target_committed')
  assert.equal(JSON.stringify(receipt).includes('0123456789abcdef'),false)
  assert.equal(JSON.stringify(f.plan).includes(value),false)
  assert.deepEqual(baseEnvPatchPlanFromReceipt(f.plan.receiptPath,receipt,f.backupRoot),f.plan)
  validateBaseEnvPatchReceipt(receipt,f.plan,f.backupRoot)
  const ledger = {businessEnvironment:{active:f.plan,previous:null}}
  const reverse = await prepareQualificationRollbackBaseEnvPatch({baseEnv:f.baseEnv,receiptPath:f.plan.receiptPath,operationLock:f.lock,targetReleaseCommit:'b'.repeat(40),rollbackReleaseCommit:'a'.repeat(40),activeLedgerGeneration:2,activeLedger:ledger,ownerUid:uid,backupRoot:f.backupRoot})
  await restorePriorBaseEnv(f.lock,reverse,uid,{backupRoot:f.backupRoot,requireReceipt:true})
  assert.deepEqual(await readFile(f.baseEnv),f.before)
  await commitPriorBaseEnvRestore(f.lock,reverse,uid,{backupRoot:f.backupRoot,requireReceipt:true})
  await assert.rejects(installTargetBaseEnvPatch(f.lock,reverse,uid,{backupRoot:f.backupRoot}),/forward resume/)
  assert.deepEqual(await readFile(f.baseEnv),f.before)
  // Failed reverse activation must reinstall the exact target before it starts.
  await reinstallCommittedTargetBaseEnvPatch(f.lock,reverse,uid,{backupRoot:f.backupRoot})
  assert.equal(businessDatabaseEnvState(await readFile(f.baseEnv)),hash(value))
  await commitTargetBaseEnvPatch(f.lock,reverse,uid,{backupRoot:f.backupRoot})
})

test('kill after env rename but before receipt resumes from bound backup', async t => {
  const f = await fixture(t)
  await writeFile(f.plan.backupPath,f.before,{mode:0o600})
  await writeFile(f.baseEnv,businessDatabaseEnvPatch(f.before,value).after,{mode:0o600})
  const recovered = await installTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot})
  assert.equal(recovered.state,'target_installed')
  await restorePriorBaseEnv(f.lock,f.plan,uid,{backupRoot:f.backupRoot})
  assert.deepEqual(await readFile(f.baseEnv),f.before)
})

test('intent before install recovers without inventing receipt; mutated secret cannot install', async t => {
  const f = await fixture(t)
  assert.equal(await restorePriorBaseEnv(f.lock,f.plan,uid,{backupRoot:f.backupRoot}),null)
  assert.equal(await commitPriorBaseEnvRestore(f.lock,f.plan,uid,{backupRoot:f.backupRoot}),null)
  await writeFile(f.plan.valuePath,value.replace('0123456789abcdef','changed'))
  await assert.rejects(installTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot}),/digest/)
  assert.deepEqual(await readFile(f.baseEnv),f.before)
})

test('private value mode and symlink are rejected before environment mutation', async t => {
  const f=await fixture(t)
  await chmod(f.plan.valuePath,0o644)
  await assert.rejects(installTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot}),/private/)
  await unlink(f.plan.valuePath);await symlink(f.valueFile,f.plan.valuePath)
  await assert.rejects(installTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot}),/private/)
  assert.deepEqual(await readFile(f.baseEnv),f.before)
})

test('Business config lineage survives successors and blocks an unreceipted absent boundary', async t => {
  const f=await fixture(t)
  const activated=nextBusinessEnvironment(null,f.plan,'activate','activated')
  const first={businessEnvironment:activated}
  assert.throws(()=>nextBusinessEnvironment(first,null,null,'rolledBack'),/boundary/)
  const next=nextBusinessEnvironment(first,null,null,'activated')
  assert.deepEqual(next,{active:f.plan,previous:f.plan})
  assert.deepEqual(nextBusinessEnvironment({businessEnvironment:next},null,null,'rolledBack'),next)
  assert.deepEqual(nextBusinessEnvironment(first,f.plan,'rollback','rolledBack'),{active:null,previous:f.plan})
  assert.throws(()=>nextBusinessEnvironment(first,f.plan,'activate','activated'),/already/)
  const body=businessDatabaseEnvPatch(f.before,value).after
  assertBusinessEnvironmentBinding(first,body,hash(value))
  assert.throws(()=>assertBusinessEnvironmentBinding(first,body,null),/disagree/)
  assert.throws(()=>assertBusinessEnvironmentBinding(null,body,hash(value)),/disagree/)
  assert.throws(()=>assertBusinessEnvironmentBinding(first,Buffer.concat([body,Buffer.from('EXTRA=1\n')]),hash(value)),/disagree/)
})

test('inherited Business binding rejects changed or missing runtime before private verification succeeds', async t => {
  const f=await fixture(t)
  const ledger={businessEnvironment:{active:f.plan,previous:f.plan}}
  await installTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot})
  await verifyBusinessPrivateEnvironment(ledger,f.baseEnv,{[key]:value},uid)
  await assert.rejects(verifyBusinessPrivateEnvironment(ledger,f.baseEnv,{},uid),/disagree/)
  await assert.rejects(verifyBusinessPrivateEnvironment(ledger,f.baseEnv,{[key]:value.replace('0123456789abcdef','changed')},uid),/disagree/)
  await assert.rejects(verifyBusinessPrivateEnvironment(null,f.baseEnv,{[key]:value},uid),/disagree/)
  await assert.rejects(assertBusinessHostBinding(ledger,f.valueFile,uid),/path/)
})

test('interrupted ordinary successor cannot cross startup or ingress with inherited env drift', async t => {
  const f=await fixture(t)
  const ledger={businessEnvironment:{active:f.plan,previous:f.plan}}
  await installTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot})
  // There is no new baseEnvPatch in an ordinary successor. The inherited
  // ledger remains its authority after every durable restart point.
  await writeFile(f.baseEnv,Buffer.concat([await readFile(f.baseEnv),Buffer.from('DRIFT=1\n')]))
  for(const phase of ['prepared','base_env_patched','private_started','private_verified','ledger_written']) {
    const events=[]
    const guarded=name=>async()=>{await assertBusinessHostBinding(ledger,f.baseEnv,uid);events.push(name)}
    await assert.rejects(executeDurableReleasePhaseMachine({phase,transition:{},advance:async()=>{},effects:{
      stopIngress:async()=>{},installTargetBaseEnv:async()=>{},assertTargetBaseEnv:guarded('assert'),preflightTarget:guarded('preflight'),
      activateTarget:guarded('activate'),verifyTargetRuntime:guarded('runtime'),startTargetPrivate:guarded('start'),verifyTargetPrivate:guarded('private'),
      writeTargetLedger:guarded('ledger'),openTargetIngress:guarded('ingress'),verifyTargetExternal:guarded('external')
    }}),/disagree/)
    assert.deepEqual(events,[])
  }
})

test('interrupted ordinary recovery refuses changed inherited env before start, ledger or ingress', async t => {
  const f=await fixture(t)
  const ledger={businessEnvironment:{active:f.plan,previous:f.plan}}
  await installTargetBaseEnvPatch(f.lock,f.plan,uid,{backupRoot:f.backupRoot})
  await writeFile(f.baseEnv,f.before)
  for(const phase of ['recovery_env_restored','recovery_private_started','recovery_private_verified','recovery_ledger_restored']) {
    const events=[]
    const guarded=name=>async()=>{await assertBusinessHostBinding(ledger,f.baseEnv,uid);events.push(name)}
    await assert.rejects(executeDurableReleaseRecoveryPhaseMachine({phase,advance:async()=>{},effects:{
      restoreTargetData:async()=>{},restoreRecoveryBaseEnv:async()=>{},verifyRollbackRuntime:guarded('runtime'),startRollbackPrivate:guarded('start'),
      verifyRollbackPrivate:guarded('private'),restoreLedger:guarded('ledger'),openRollbackIngress:guarded('ingress'),verifyRollbackExternal:guarded('external')
    }}),/disagree/)
    assert.deepEqual(events,[])
  }
})


test('Business journal binds config transitions across every durable phase and rejects lost successor lineage', () => {
  const target='a'.repeat(40), prior='b'.repeat(40), token='business-transaction'
  const backup='/opt/meetingassist-backups', stem=`base-env-${target}-${token}`
  const plan={schema:'bonfire.business-env-patch.v1',transactionToken:token,targetReleaseCommit:target,rollbackReleaseCommit:prior,targetLedgerGeneration:2,baseEnvPath:'/opt/meetingassist/deploy/digitalocean/.env',backupDir:backup,backupPath:`${backup}/${stem}.before.env`,receiptPath:`${backup}/${stem}.receipt.json`,patchKey:key,priorQualificationState:'absent',beforeSha256:hash('before'),afterSha256:hash('after'),valuePath:`${backup}/${stem}.value`,valueSha256:hash(value)}
  const entry=c=>({releaseDir:`/opt/meetingassist-releases/${c.repeat(40)}`,releaseCommit:c.repeat(40),bundleSha256:hash(c),meetingassistImageId:`sha256:${hash(c)}`,renderRunnerImageId:`sha256:${hash(c+'render')}`})
  const original={schema:'bonfire.active-release-ledger.v1',generation:1,updatedAt:'2026-09-05T12:00:00Z',active:entry('b'),previous:entry('c')}
  const enabled={...original,generation:2,active:entry('a'),previous:entry('b'),businessEnvironment:{active:plan,previous:null}}
  const journal={schema:'bonfire.release-transaction.v2',token,action:'activated',phase:'prepared',targetBundleSha256:hash('a'),rollbackBundleSha256:hash('b'),targetRenderedComposeSha256:hash('compose-a'),rollbackRenderedComposeSha256:hash('compose-b'),priorLedger:original,nextLedger:enabled,baseEnvPatch:plan,baseEnvPatchMode:'activate',recoveryFromPhase:null,baselineProjectContainers:[],baselineProjectResources:{networks:[],volumes:[]},createdAt:'2026-09-05T12:00:00Z',updatedAt:'2026-09-05T12:00:00Z'}
  for(const phase of ['prepared','ingress_stopped','base_env_patch_started','base_env_patched','target_preflighted','data_transition_started','data_ready','private_started','private_verified','ledger_written','ingress_opened','external_verified']) assert.equal(validateReleaseTransactionJournal({...journal,phase},{token}).phase,phase)
  for(const phase of ['recovery_started','recovery_data_restored','recovery_env_restore_started','recovery_env_restored','recovery_runtime_verified','recovery_private_started','recovery_private_verified','recovery_ledger_restored','recovery_ingress_opened','recovery_external_verified']) assert.equal(validateReleaseTransactionJournal({...journal,phase,recoveryFromPhase:'private_started'},{token}).phase,phase)
  const lost={...enabled};delete lost.businessEnvironment
  assert.throws(()=>validateReleaseTransactionJournal({...journal,nextLedger:lost},{token}),/lineage/)
  const successor={...enabled,generation:3,active:entry('d'),previous:entry('a'),businessEnvironment:{active:plan,previous:plan}}
  validateReleaseTransactionJournal({...journal,priorLedger:enabled,nextLedger:successor,baseEnvPatch:null,baseEnvPatchMode:null},{token})
  const noLineage={...successor};delete noLineage.businessEnvironment
  assert.throws(()=>validateReleaseTransactionJournal({...journal,priorLedger:enabled,nextLedger:noLineage,baseEnvPatch:null,baseEnvPatchMode:null},{token}),/lineage/)
  const completion=releaseTransactionCompletionEvidence(journal,'prepared')
  assert.equal(completion.businessDatabaseReceipt,plan.receiptPath)
  assert.equal(completion.qualificationReceipt,undefined)
  assert.equal(JSON.stringify(completion).includes(value),false)
  const runtimePlan={...plan}
  verifyBaseEnvPatchRuntimeEnvironment({[key]:value},runtimePlan,'target')
  verifyBaseEnvPatchRuntimeEnvironment({},runtimePlan,'prior')
  assert.throws(()=>verifyBaseEnvPatchRuntimeEnvironment({[key]:value},runtimePlan,'prior'))
})
