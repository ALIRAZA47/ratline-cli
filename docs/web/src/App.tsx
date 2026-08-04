import { Route, Routes } from 'react-router-dom';
import { Layout } from './components/Layout';
import { commandGroups } from './data/nav';

import { Home } from './pages/Home';
import { Quickstart } from './pages/Quickstart';
import { ReferenceIndex } from './pages/ReferenceIndex';
import { CommandGroupPage } from './pages/CommandGroupPage';
import { GlobalFlags } from './pages/GlobalFlags';
import { ExitCodesPage } from './pages/ExitCodesPage';
import { JsonEnvelope } from './pages/JsonEnvelope';
import { ConfigReference } from './pages/ConfigReference';
import { ValidationPage } from './pages/ValidationPage';
import { NotFound } from './pages/NotFound';

import { ConceptModel } from './pages/concepts/Model';
import { ConceptSshScopes } from './pages/concepts/SshScopes';
import { ConceptTlsLifecycle } from './pages/concepts/TlsLifecycle';
import { ConceptRateLimits } from './pages/concepts/RateLimits';
import { ConceptTransactions } from './pages/concepts/Transactions';
import { ConceptFilesystem } from './pages/concepts/Filesystem';
import { ConceptSupervision } from './pages/concepts/Supervision';
import { ConceptSecurity } from './pages/concepts/Security';
import { ConceptInteractive } from './pages/concepts/Interactive';

import { GuideFastApi } from './pages/guides/FastApi';
import { GuideNextJs } from './pages/guides/NextJs';
import { GuideAstro } from './pages/guides/Astro';
import { GuideContractorAccess } from './pages/guides/ContractorAccess';
import { GuideNewLaptopKey } from './pages/guides/NewLaptopKey';
import { GuideCiDeployKeys } from './pages/guides/CiDeployKeys';
import { GuideIssueCert } from './pages/guides/IssueCert';
import { GuideCloudflare } from './pages/guides/Cloudflare';
import { GuideRenewalRunbook } from './pages/guides/RenewalRunbook';
import { GuideSshLockout } from './pages/guides/SshLockout';
import { GuideDebug502 } from './pages/guides/Debug502';

export default function App() {
  return (
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<Home />} />
        <Route path="quickstart" element={<Quickstart />} />

        <Route path="reference" element={<ReferenceIndex />} />
        <Route path="reference/global-flags" element={<GlobalFlags />} />
        <Route path="reference/exit-codes" element={<ExitCodesPage />} />
        <Route path="reference/json" element={<JsonEnvelope />} />
        <Route path="reference/validation" element={<ValidationPage />} />
        <Route path="reference/config" element={<ConfigReference />} />
        {/* One page per command group, all driven by the same typed data. */}
        {commandGroups.map((g) => (
          <Route
            key={g.id}
            path={g.path.replace(/^\//, '')}
            element={<CommandGroupPage />}
          />
        ))}

        <Route path="concepts/model" element={<ConceptModel />} />
        <Route path="concepts/ssh-scopes" element={<ConceptSshScopes />} />
        <Route path="concepts/tls-lifecycle" element={<ConceptTlsLifecycle />} />
        <Route path="concepts/rate-limits" element={<ConceptRateLimits />} />
        <Route path="concepts/transactions" element={<ConceptTransactions />} />
        <Route path="concepts/filesystem" element={<ConceptFilesystem />} />
        <Route path="concepts/supervision" element={<ConceptSupervision />} />
        <Route path="concepts/security" element={<ConceptSecurity />} />
        <Route path="concepts/interactive" element={<ConceptInteractive />} />

        <Route path="guides/fastapi" element={<GuideFastApi />} />
        <Route path="guides/nextjs" element={<GuideNextJs />} />
        <Route path="guides/astro" element={<GuideAstro />} />
        <Route path="guides/contractor-access" element={<GuideContractorAccess />} />
        <Route path="guides/new-laptop-key" element={<GuideNewLaptopKey />} />
        <Route path="guides/ci-deploy-keys" element={<GuideCiDeployKeys />} />
        <Route path="guides/issue-cert" element={<GuideIssueCert />} />
        <Route path="guides/cloudflare" element={<GuideCloudflare />} />
        <Route path="guides/renewal-runbook" element={<GuideRenewalRunbook />} />
        <Route path="guides/ssh-lockout" element={<GuideSshLockout />} />
        <Route path="guides/debug-502" element={<GuideDebug502 />} />

        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
