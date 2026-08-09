import assert from 'node:assert/strict'
import test from 'node:test'
import { generateKeyPairSync, sign } from 'node:crypto'
import { validateW8ReadOnlyEnvelope } from './e10-w8-readonly-preflight.mjs'

const cohorts=['organization_profile_private','contribution_work_record_private','network_publication','evidence_search','network_contact']
const resultPrivateKey=`-----BEGIN PRIVATE KEY-----
MC4CAQAwBQYDK2VwBCIEIFYsu80/y5IE1OjvlVhdIZL3YBHF83WNg7rJ7SN9XiHc
-----END PRIVATE KEY-----
`
const sealedPolicy={schema:'stride.e10.w8.node-root-policy.v1',rootKeyId:'stride-e10-w8-root-2026-08',resultKeyId:'pinned-w8-result',resultPublicKey:`-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEAZko78rknXOWP5/p0oQFck/98nBOr08sfOjVM55leQrk=
-----END PUBLIC KEY-----
`,trustPolicyDigest:'a'.repeat(64),manifestDigest:'b'.repeat(64),w7ManifestDigest:'c'.repeat(64),signature:'EbcV23jfCmOtaXJ8jeSVxB2ELF//0rHej4SM7msMqfcQLt0ZTK4JLU4SH5GV4dBdnj9q+mo2YPctuDwBpn+pCQ=='}
function envelope(policy=sealedPolicy,key=resultPrivateKey){const e={schema:'stride.e10.w8.validator-result-envelope.v1',keyId:policy.resultKeyId,trustPolicyDigest:policy.trustPolicyDigest,manifestDigest:policy.manifestDigest,w7ManifestDigest:policy.w7ManifestDigest,payload:{validator:'ValidateStrideE10W8Activation',ready:true,reasons:[],candidateCommit:'d'.repeat(40),dependencyReceiptsVerified:true,cohorts,soakHours:24,sittings:10,sourceMode:'production_signed_receipts'},signature:''};const input=Buffer.from(`meetingassist/stride/e10/w8/validator-result/v1\0${e.schema}\0${e.keyId}\0${e.trustPolicyDigest}\0${e.manifestDigest}\0${e.w7ManifestDigest}\0${JSON.stringify(e.payload)}`);e.signature=sign(null,input,key).toString('base64');return e}
test('W8 adapter rejects replacement roots and non-finite soak',()=>{assert.equal(validateW8ReadOnlyEnvelope(envelope(),sealedPolicy).ready,true);for(const value of [Infinity,NaN,'24']){const e=envelope();e.payload.soakHours=value;const input=Buffer.from(`meetingassist/stride/e10/w8/validator-result/v1\0${e.schema}\0${e.keyId}\0${e.trustPolicyDigest}\0${e.manifestDigest}\0${e.w7ManifestDigest}\0${JSON.stringify(e.payload)}`);e.signature=sign(null,input,resultPrivateKey).toString('base64');assert.equal(validateW8ReadOnlyEnvelope(e,sealedPolicy).ready,false)}const attackerRoot=generateKeyPairSync('ed25519'),attackerResult=generateKeyPairSync('ed25519'),replaced={...sealedPolicy,resultPublicKey:attackerResult.publicKey.export({type:'spki',format:'pem'}),signature:''};const policyInput=Buffer.from(`meetingassist/stride/e10/w8/node-root-policy/v1\0${replaced.schema}\0${replaced.rootKeyId}\0${replaced.resultKeyId}\0${replaced.resultPublicKey}\0${replaced.trustPolicyDigest}\0${replaced.manifestDigest}\0${replaced.w7ManifestDigest}`);replaced.signature=sign(null,policyInput,attackerRoot.privateKey).toString('base64');assert.equal(validateW8ReadOnlyEnvelope(envelope(replaced,attackerResult.privateKey),replaced).ready,false)})
