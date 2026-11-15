/** Fixed operation dispatch; callers cannot choose a URL or transport.
 * @param {any} broker @param {string} operation @param {any} input */
export function dispatchResource(broker, operation, input) {
  switch (operation) {
    case 'listResourcePackages':
      return broker.call('listResourcePackages', input);
    case 'createResourcePackage':
      return broker.call('createResourcePackage', input);
    case 'getResourcePackage':
      return broker.call('getResourcePackage', input);
    case 'patchResourcePackage':
      return broker.call('patchResourcePackage', input);
    case 'listResourceVersions':
      return broker.call('listResourceVersions', input);
    case 'uploadResourceVersion':
      return broker.call('uploadResourceVersion', input);
    case 'getResourceVersion':
      return broker.call('getResourceVersion', input);
    case 'submitResourceVersion':
      return broker.call('submitResourceVersion', input);
    case 'requestResourceDistribution':
      return broker.call('requestResourceDistribution', input);
    case 'getResourceDistribution':
      return broker.call('getResourceDistribution', input);
    case 'downloadResourceContent':
      return broker.call('downloadResourceContent', input);
    case 'getResourceRevocations':
      return broker.call('getResourceRevocations', input);
    case 'createMemberResourceDownloadTicket':
      return broker.call('createMemberResourceDownloadTicket', input);
    case 'consumeMemberResourceDownloadTicket':
      return broker.call('consumeMemberResourceDownloadTicket', input);
    case 'listMemberResourceInstallations':
      return broker.call('listMemberResourceInstallations', input);
    case 'selectMemberResourceInstallation':
      return broker.call('selectMemberResourceInstallation', input);
    case 'reportMemberResourceInstallation':
      return broker.call('reportMemberResourceInstallation', input);
    case 'UnifiedListResourcePackages':
      return broker.call('UnifiedListResourcePackages', input);
    case 'UnifiedCreateResourcePackage':
      return broker.call('UnifiedCreateResourcePackage', input);
    case 'UnifiedGetResourcePackage':
      return broker.call('UnifiedGetResourcePackage', input);
    case 'UnifiedListResourceVersions':
      return broker.call('UnifiedListResourceVersions', input);
    case 'UnifiedUploadResourceVersion':
      return broker.call('UnifiedUploadResourceVersion', input);
    case 'UnifiedGetResourceVersion':
      return broker.call('UnifiedGetResourceVersion', input);
    case 'UnifiedSubmitResourceVersion':
      return broker.call('UnifiedSubmitResourceVersion', input);
    case 'UnifiedDownloadResourceManifest':
      return broker.call('UnifiedDownloadResourceManifest', input);
    case 'UnifiedCreateDownloadTicket':
      return broker.call('UnifiedCreateDownloadTicket', input);
    case 'UnifiedDownloadArtifact':
      return broker.call('UnifiedDownloadArtifact', input);
    case 'UnifiedListInstallations':
      return broker.call('UnifiedListInstallations', input);
    case 'UnifiedSelectInstallation':
      return broker.call('UnifiedSelectInstallation', input);
    case 'UnifiedReportInstallation':
      return broker.call('UnifiedReportInstallation', input);
    case 'UnifiedRenewInstallation':
      return broker.call('UnifiedRenewInstallation', input);
    case 'UnifiedDownloadVersionArtifact':
      return broker.call('UnifiedDownloadVersionArtifact', input);
    default:
      throw Object.assign(new Error('common.validation_failed'), {
        code: 'common.validation_failed',
      });
  }
}
