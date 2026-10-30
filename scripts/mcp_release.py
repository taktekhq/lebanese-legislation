"""Choose an immutable MCP wrapper release for a pinned published API image."""
import json
import os
import re
import urllib.request


def select_release(version, source_sha, api_digest, versions):
    if not re.fullmatch(r'\d+\.\d+\.\d+',version) or not re.fullmatch('[0-9a-f]{40}',source_sha) or not re.fullmatch('sha256:[0-9a-f]{64}',api_digest):
        raise ValueError('invalid release identity')
    def key(value):return tuple(map(int,value.split('.')))
    existing=any(version in tags for tags in versions)
    current=[]
    for tags in versions:
        if 'latest' in tags:
            found=[tag for tag in tags if re.fullmatch(r'\d+\.\d+\.\d+',tag)]
            if not found:raise ValueError('latest has no immutable version identity')
            current.extend(found)
    skip=bool(current and max(map(key,current))>=key(version))
    candidate=version if existing else 'source-'+source_sha+'-api'+version+'-'+api_digest[7:19]
    return {'version':version,'candidate':candidate,'build':not existing,'skip':skip}


def main():
    version=os.environ['API_VERSION'].strip()
    requested=os.getenv('REQUESTED_VERSION','auto') or 'auto'
    if requested!='auto' and requested!=version:
        raise ValueError('requested version differs from pinned API image')
    versions=[]
    for page in range(1,101):
        url=f'https://api.github.com/orgs/taktekhq/packages/container/lebanese-legislation-mcp/versions?per_page=100&page={page}'
        request=urllib.request.Request(url,headers={'Authorization':'Bearer '+os.environ['GH_TOKEN'],'Accept':'application/vnd.github+json'})
        with urllib.request.urlopen(request,timeout=30) as response:items=json.load(response)
        versions.extend(item['metadata']['container']['tags'] for item in items)
        if len(items)<100:break
    else:raise ValueError('package version listing exceeded safety bound')
    result=select_release(version,os.environ['GITHUB_SHA'],os.environ['API_DIGEST'],versions)
    with open(os.environ['GITHUB_OUTPUT'],'a') as out:
        for name,value in result.items():out.write(name+'='+str(value).lower()+'\n')


if __name__=='__main__':main()
