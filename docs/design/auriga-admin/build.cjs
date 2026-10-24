const fs=require('node:fs'),path=require('node:path');
const read=name=>fs.readFileSync(path.join(__dirname,name),'utf8');
const script=read('prototype.js').replace('window.addEventListener(\'hashchange\',readRoute);readRoute();','')+'\n'+read('models.js')+'\n'+read('headquarters.js')+'\nwindow.addEventListener(\'hashchange\',readRoute);readRoute();';new Function(script);
const html=read('prototype.html').replace('/*STYLE*/',()=>read('prototype.css')).replace('/*APP*/',()=>script);
fs.writeFileSync(path.join(__dirname,'index.html'),html);
console.log('Built standalone admin prototype: '+Buffer.byteLength(html)+' bytes');
