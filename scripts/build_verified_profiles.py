"""Rebuild reviewed v2 profiles from the ten supplied sources (never edits originals).

Indices and wording below are specific to audited originals. Changing a source requires
reviewing this mapping, a new profile revision and a new template registration.
"""
import hashlib
import json
import re
from pathlib import Path
from zipfile import ZipFile
from lxml import etree

ROOT = Path(__file__).resolve().parents[1]
OUT = ROOT / 'docs/template-profiles/v2'
W = '{http://schemas.openxmlformats.org/wordprocessingml/2006/main}'
def original(pattern): return next((ROOT/'data/demo').glob(pattern))
def source(pattern,section):
    path = original(pattern)
    return dict(name=path.name,version='sha256:'+hashlib.sha256(path.read_bytes()).hexdigest(),section=section)
METHOD = lambda s: source('Методические*.docx',s)
INSTRUCTION = lambda s: source('Инструкция*.pdf',s)
MATRIX = lambda s: source('Матрица*.docx',s)
def lit(v): return {'literal':v}
def field(k): return {'field':k}
def cond(k,v='yes'): return dict(op='equals',field=k,value=v)
def present(k): return dict(op='present',field=k)
def branch(c,a,b=()): return {'when':c,'then':list(a),'otherwise':list(b)}
def choice(k,a,b=()): return branch(cond(k),a,b)
def both(*args): return dict(op='all',args=list(args))
def anyof(*args): return dict(op='any',args=list(args))
def compare(op,a,b): return dict(op=op,field=a,other_field=b)


class Builder:
    def __init__(self,id,pattern,notes,last,kind):
        self.path=original(pattern)
        with ZipFile(self.path) as z: self.root=etree.fromstring(z.read('word/document.xml'))
        self.ps=[''.join(t.text or '' for t in n.iter(W+'t')) for n in self.root.iter(W+'p')]
        self.tables=list(self.root.iter(W+'tbl'))
        self.p=dict(profile_version=2,profile_id=id,revision='2.0.2',rules_version='2026-10-24.3',
                    engine_version='docx-profile/2.0.0',
                    template_kind=kind,source_sha256=hashlib.sha256(self.path.read_bytes()).hexdigest(),
                    field_source=source(pattern,'Примечания по заполнению; раздел «Общие правила»'),
                    fields=[],bindings=[],conditional_blocks=[],repeaters=[],numbered_paragraphs=[],rules=[],approval_route=[])
        anchor=lambda i:dict(paragraph=i,text=self.ps[i])
        approval=''.join(t.text or '' for t in self.tables[0].iter(W+'t'))
        protected=[anchor(i) for i,t in enumerate(self.ps) if i < notes and 'НЕ УДАЛЯТЬ' in t]
        self.p['document_cleanup']=dict(approval_table=0,approval_text=approval,marker=anchor(5),
                    notes_start=anchor(notes),notes_end=anchor(last),protected=protected,
                    remove_paragraphs=[anchor(notes-1)])
        self.anchor=anchor
        self.f('organization','Наименование организации')
        self.edit(8,[field('organization')])
        self.f('signer','Подписант: инициалы и фамилия',type='person',description='Выберите уполномоченного подписанта; фактические полномочия и подписание проверяются вне генератора.')
        self.f('module_current_confirmed','Актуальность модуля проверена в реестре?',type='choice',required=False,options=['yes','no'])
        self.f('signer_authority_confirmed','Полномочия подписанта проверены?',type='choice',required=False,options=['yes','no'])
        self.f('affected_departments','Дополнительные подразделения по затронутой компетенции',type='text_list',required=False)
        self.rule('module.currency','warning','Проверьте актуальный статус, редакцию и начало применения модуля в реестре. Ответ пользователя не заменяет проверку реестра.',METHOD('1.2'),check=cond('module_current_confirmed'))
        self.rule('signer.authority','manual_review','Проверьте полномочия подписанта, должность и оформление подписи. Генератор не выполняет подписание.',INSTRUCTION('8.19–8.20; 10.15'),check=cond('signer_authority_confirmed'))
        self.rule('document.review','manual_review','Сопоставьте постоянные формулировки, грамматику, приложения и оформление с модулем; при отличии определите полный маршрут. Предусмотрите лингвистическую экспертизу.',METHOD('1.3; 3.3; 4.1–4.3'))
        self.rule('module.application','manual_review','Проверьте условия применения модуля, общий маршрут визирования и лингвистическую экспертизу. Автоматическое соответствие профилю не устанавливает юридическую применимость и не выполняет согласование.',INSTRUCTION('9.16–9.16.3; 10.6; 10.11; 10.13'))
        self.rule('sources.educational','info','Инструкция — учебный пример, не действующий документ. Актуальность и применимость официальных правил автоматически не установлены.',INSTRUCTION('Маркировка страниц; 12.10–12.11'))
        self.rule('project.registration','info','Это проект. Регистрация, номер, подпись, ЭП и визы выполняются в установленном порядке после проверки.',METHOD('2.1; 5.1–5.3'))
        self.rule('structure.cleanup','info','При генерации удаляются служебные элементы модуля по закреплённым структурным якорям; дата, регистрационный номер и визуализация ЭП сохраняются.',METHOD('3.1–3.3'))
    def f(self,key,label,type='text',required=True,description='',**kw):
        if type in ('repeater','person_list','text_list'):
            kw.setdefault('minimum_items',1); kw.setdefault('maximum_items',30)
        for item in kw.get('item_fields',[]):
            item.setdefault('required',True); item.setdefault('max_length',1000)
        self.p['fields'].append(dict(key=key,label=label,type=type,required=required,question=label+'?',description=description,max_length=2000,**kw))
    def edit(self,i,ss): self.p['bindings'].append(dict(paragraph=i,match=self.ps[i],value=ss))
    def replace(self,i,match,ss):
        assert self.ps[i].count(match)==1,(i,match)
        self.p['bindings'].append(dict(paragraph=i,match=match,value=ss))
    def single(self,i,key,suffix=''):
        # Reviewed single variable paragraph: preserve constant wording, remove only its note.
        text=re.sub(r'\s*\[\d+\]','',self.ps[i])
        matches=list(re.finditer(r'_{3,}',text)); assert len(matches)==1,(i,text)
        m=matches[0]
        self.edit(i,[lit(text[:m.start()]),field(key),lit(suffix+text[m.end():])])
    def rule(self,id,category,description,src,**kw): self.p['rules'].append(dict(id=id,category=category,description=description,source=src,**kw))
    def local_rule(self,id,category,description,section,**kw): self.rule(id,category,description,dict(self.p['field_source'],section=section),**kw)
    def person(self,key,label): self.f(key,label,type='person',description='Должность, полное наименование подразделения, инициалы и фамилия; в грамматической форме исходной фразы.')
    def signer(self,position,name):
        self.f('signer_position','Должность подписанта')
        self.edit(position,[field('signer_position')]); self.edit(name,[field('signer')])
    def participants(self,key,label,table,start,end):
        items=[dict(key=k,label=l,required=req) for k,l,req in [('position','Должность',True),('unit','Подразделение',True),('name','Инициалы и фамилия',True),('agreement','По согласованию для внешнего участника, если применимо',False)]]
        self.f(key,label,type='repeater',item_fields=items,maximum_items=30,description='По одной записи на участника. Для внешних участников проверьте необходимость согласования.')
        self.repeater(key,table,start,end,[[lit(label)],[field('position'),lit(', '),field('unit'),lit(', '),field('name'),branch(present('agreement'),[lit(' ('),field('agreement'),lit(')')])]])
    def repeater(self,key,table,start,end,cells):
        row=self.tables[table].findall(W+'tr')[start]
        self.p['repeaters'].append(dict(field=key,table=table,start_row=start,end_row=end,anchor=''.join(t.text or '' for t in row.iter(W+'t')),cells=cells,keep_rows_together=True,repeat_header=True))
    def repeal(self,index,heading=False):
        self.f('repeal','Отменяется прежний акт?',type='choice',options=['yes','no'])
        for k,label,t in [('old_act_type','Вид прежнего акта','text'),('old_act_date','Дата прежнего акта','date'),('old_act_number','Номер прежнего акта','text'),('old_act_name','Полное наименование прежнего акта','text')]:
            options=['распоряжение Организации'] if heading else ['приказ Организации'] if self.p['profile_id']=='records-responsibles' else ['приказ Организации','распоряжение Организации']
            self.f(k,label,type='choice' if k=='old_act_type' else t,required=False,required_if=cond('repeal'),visible_if=cond('repeal'),**({'options':options} if k=='old_act_type' else {}))
        self.p['conditional_blocks'].append(dict(start=self.anchor(index),end=self.anchor(index),include_if=cond('repeal')))
        prefix=self.ps[index].split('.')[0]+'. '
        verb='Отменить ' if heading else 'Признать утратившим силу '
        self.edit(index,[lit(prefix+verb),*self.oldact(),lit('.')])
    def oldact(self,genitive=False):
        type_segments=[branch(cond('old_act_type','приказ Организации'),[lit('приказа Организации')]),branch(cond('old_act_type','распоряжение Организации'),[lit('распоряжения Организации')])] if genitive else [field('old_act_type')]
        return [*type_segments,lit(' от '),field('old_act_date'),lit(' № '),field('old_act_number'),lit(' «'),field('old_act_name'),lit('»')]
    def control(self,i,number,kind='приказа'):
        self.f('self_control','Контроль остаётся за подписантом?',type='choice',options=['yes','no'])
        self.person('controller','Контролирующее лицо')
        f=self.p['fields'][-1]; f.update(required=False,required_if=cond('self_control','no'),visible_if=cond('self_control','no'))
        self.edit(i,[lit(f'{number}. Контроль за исполнением настоящего {kind} '),choice('self_control',[lit('оставляю за собой.')],[lit('возложить на '),field('controller'),lit('.')])])
    def numbering(self,*indices): self.p['numbered_paragraphs']=[self.anchor(i) for i in indices]
    def route(self,row,description,required,conditional=(),when=None):
        r=dict(id='internal.'+str(row),description=description,required_departments=required,conditional_departments=list(conditional),source=MATRIX('Положения 1–3; внутренние документы, строка '+str(row)))
        if when: r['when']=when
        self.p['approval_route'].append(r)
    def temporary_route(self):
        self.f('it_related','Объединение создаётся для ИТ-проекта или вопросов ИС?',type='choice',options=['yes','no'])
        self.route(6,'Рабочие группы, проектные комитеты, комиссии и временные объединения',['Департамент ДОУ','Кадровое подразделение'],[dict(name='Департамент информационных технологий',condition='Создание объединения для ИТ-проекта или решения вопросов ИС',when=cond('it_related'))])
    def save(self):
        notes={
            'working-group':[(1,'purpose'),(2,'group_subject'),(3,'leader secretary members'),(4,'deadline actions'),(5,'result addressee'),(6,'termination'),(7,'controller'),(8,'signer signer_position')],
            'project-committee':[(1,'projects'),(3,'purpose'),(4,'basis'),(6,'responsible_unit'),(8,'chair deputy secretary members'),(9,'repeal old_act_type old_act_date old_act_number old_act_name'),(10,'controller self_control')],
            'is-commissioning':[(1,'system_full_name'),(2,'readiness_document readiness_date readiness_number'),(4,'system_short_name'),(5,'change_kind'),(6,'development_document development_date development_number'),(8,'commissioning_date date_exception date_exception_basis'),(9,'customer'),(11,'purpose'),(12,'operating_docs_confirmation'),(14,'responsible'),(15,'signer signer_position')],
            'local-document':[(1,'purpose'),(2,'document_type document_name'),(3,'effective_date transition'),(4,'responsible_unit'),(5,'location'),(6,'repeal old_act_type old_act_date old_act_number old_act_name'),(7,'controller self_control'),(8,'signer signer_position')],
            'records-responsibles':[(1,'basis'),(2,'responsibles'),(3,'notification_recipient'),(4,'coordinator'),(5,'repeal old_act_type old_act_date old_act_number old_act_name'),(6,'controller self_control'),(7,'signer signer_position')],
            'property-transfer':[(1,'basis'),(2,'giver'),(3,'receiver'),(4,'items'),(5,'include_source transfer_source'),(6,'representative'),(7,'controller self_control'),(8,'signer signer_position')],
            'internal-audit':[(1,'basis'),(2,'processes start_date end_date'),(3,'leader auditors independence_confirmed'),(4,'unit access_scope'),(5,'report_deadline report_addressee'),(6,'plan_owner plan_deadline'),(7,'controller'),(8,'signer signer_position')],
        }
        sections={k:'Примечание '+str(n) for n,keys in notes[self.p['profile_id']] for k in keys.split()}
        hints={
            'working-group':{'group_subject':'В форме после «по»; одна тема для заголовка и пункта 1.','purpose':'В родительном падеже после «В целях».','controller':'В винительном падеже после «возложить на».','addressee':'В дательном падеже; кому представить результат.'},
            'project-committee':{'responsible_unit':'В творительном падеже после «реализуемого/реализуемых».','purpose':'В родительном падеже после «В целях».','basis':'В творительном падеже после «в связи с»; при отсутствии весь оборот удаляется.'},
            'is-commissioning':{'system_full_name':'Официальное собственное наименование в кавычках. В преамбуле уже есть слова «компонента информационной системы»; не дублируйте их.','readiness_document':'В родительном падеже после «На основании».','development_document':'В творительном падеже после «в соответствии с».','customer':'В дательном падеже перед «обеспечить».','it_department':'В дательном падеже перед «обеспечить».','purpose':'В форме после «по следующему назначению».','responsible':'В именительном падеже: должность, подразделение, фамилия и инициалы.'},
            'local-document':{'purpose':'В родительном падеже после «В целях».','responsible_unit':'В дательном падеже перед «обеспечить».','location':'Полный оборот, например «на внутреннем портале Организации».'},
            'records-responsibles':{'basis':'В родительном падеже после «требований».','coordinator':'В дательном падеже перед «осуществлять».','notification_recipient':'В форме после «сообщать об изменениях в».'},
            'property-transfer':{'giver':'В дательном падеже перед «сдать».','receiver':'В дательном падеже перед «принять».','representative':'В винительном падеже после «Назначить».','items':'Полный перечень либо обозначение после «дела и имущество». Акт автоматически не присоединяется.'},
            'internal-audit':{'basis':'В творительном падеже после «В соответствии с».','unit':'В дательном падеже перед «обеспечить».','plan_owner':'В дательном падеже перед «подготовить».','report_addressee':'В дательном падеже; кому представить отчёт.'},
        }[self.p['profile_id']]
        for f in self.p['fields']:
            f['source']=dict(self.p['field_source'],section=sections.get(f['key'],'Общие правила'))
            if f['key']=='organization':f['source']=INSTRUCTION('8.4')
            if f['key']=='module_current_confirmed':f['source']=METHOD('1.2')
            if f['key']=='signer_authority_confirmed':f['source']=INSTRUCTION('8.19; 10.15')
            if f['key'] in ('it_related','affected_departments','personnel_change','access_changed'):f['source']=MATRIX('Положения 1–3; внутренние документы, строки 2, 6')
            if f['key'] in hints:f['description']=hints[f['key']]
            if f['key']=='controller':f['description']='В винительном падеже после «возложить на»; должность, подразделение, фамилия и инициалы.'
        OUT.mkdir(parents=True,exist_ok=True)
        path=OUT/(self.p['profile_id']+'.json'); path.write_text(json.dumps(self.p,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
        print(path.relative_to(ROOT))


def build():
    b=Builder('working-group','*создании_рабоч*.docx',34,47,'working_group_order')
    for k,l in [('group_subject','Тема рабочей группы'),('purpose','Практическая цель'),('actions','Конкретные действия'),('result','Итоговый документ'),('addressee','Адресат результата'),('termination','Событие завершения деятельности')]: b.f(k,l)
    b.f('deadline','Конечный срок',type='date')
    for k,l,i in [('leader','Руководитель',19),('secretary','Секретарь',21)]: b.person(k,l); b.edit(i,[field(k)])
    b.person('controller','Контролирующее лицо'); b.single(28,'controller')
    b.participants('members','Члены',2,3,3)
    b.single(12,'group_subject'); b.single(13,'purpose'); b.single(15,'group_subject'); b.single(27,'termination')
    b.edit(24,[lit('2. Рабочей группе до '),field('deadline'),lit(' '),field('actions'),lit(' и представить '),field('result'),lit(' '),field('addressee'),lit('.')])
    b.signer(29,31)
    b.f('coherence_confirmed','Проверена ограниченность задачи и связь цели, срока и результата?',type='choice',required=False,options=['yes','no'])
    b.local_rule('group.coherence','manual_review','Подтвердите ограниченную задачу, связь цели, срока и результата; рабочая группа не должна подменять постоянно действующий орган. Проверьте согласование внешних участников.','Прим.1–8; общие правила',check=cond('coherence_confirmed'))
    b.temporary_route(); b.save()

    b=Builder('project-committee','*проектного_комитета.docx',30,48,'project_committee_order')
    b.f('projects','Официальные наименования проектов',type='text_list')
    b.f('purpose','Цель создания'); b.f('basis','Основание создания',required=False)
    b.f('responsible_unit','Подразделение, реализующее или координирующее проекты')
    plural=dict(op='count_gt',field='projects',value=1)
    dat=[branch(plural,[lit('проектам ')],[lit('проекту ')]),field('projects')]
    gen=[branch(plural,[lit('проектов ')],[lit('проекта ')]),field('projects')]
    b.repeal(26,True)
    b.edit(12,[lit('О создании проектного комитета по '),*dat,choice('repeal',[lit(' и об отмене '),*b.oldact(genitive=True)])])
    b.edit(13,[lit('В целях '),field('purpose'),branch(present('basis'),[lit(', в связи с '),field('basis')]),lit(', для обеспечения координации и контроля реализации '),*gen,branch(plural,[lit(', реализуемых ')],[lit(', реализуемого ')]),field('responsible_unit'),lit(',')])
    b.edit(15,[lit('1. Создать Проектный комитет по '),*dat,lit(' (далее - Комитет) в составе:')])
    for k,l,i in [('chair','Председатель Комитета',19),('deputy','Заместитель председателя',21),('secretary','Секретарь',23)]: b.person(k,l); b.edit(i,[field(k)])
    b.participants('members','Члены Комитета',2,4,4)
    b.control(27,3,'распоряжения'); b.numbering(15,26,27)
    b.local_rule('committee.names','manual_review','Названия берутся из единого списка. Проверьте официальное написание, грамматические формы, должности и согласование внешних участников. Подписант задаётся в карточке проекта; блок ЭП не является выполненной подписью.','Прим.1–10; общие правила')
    b.temporary_route(); b.save()

    b=Builder('is-commissioning','*вводе_в_эксплуатацию*.docx',27,50,'is_commissioning_order')
    for k,l in [('system_full_name','Полное официальное наименование системы или компонента'),('system_short_name','Единое сокращённое наименование'),('readiness_document','Вид и название документа о готовности'),('readiness_number','Номер документа о готовности'),('development_document','Вид и название документа, определившего работы'),('development_number','Номер документа, определившего работы'),('customer','Функциональный заказчик'),('purpose','Назначение компонента'),('it_department','Официальное наименование ИТ-подразделения')]: b.f(k,l)
    for k,l in [('readiness_date','Дата документа о готовности'),('development_date','Дата документа, определившего работы'),('commissioning_date','Дата промышленного ввода')]: b.f(k,l,type='date')
    b.f('change_kind','Разработка или изменение?',type='choice',options=['developed','modified'])
    b.f('date_exception','Иной порядок даты ввода прямо предусмотрен и подтверждён?',type='choice',options=['yes','no'])
    b.f('date_exception_basis','Точные реквизиты и обоснование исключения',required=False,required_if=cond('date_exception'),visible_if=cond('date_exception'))
    b.f('operating_docs_confirmation','Сведения об утверждении/принятии эксплуатационной документации',required=False)
    b.single(12,'system_full_name')
    b.edit(13,[lit('На основании '),field('readiness_document'),lit(' от '),field('readiness_date'),lit(' № '),field('readiness_number'),lit(', содержащего решение о готовности к вводу в эксплуатацию компонента информационной системы '),field('system_full_name'),lit(' (далее - «'),field('system_short_name'),lit('»), '),branch(cond('change_kind','developed'),[lit('разработанного')],[lit('измененного')]),lit(' в соответствии с '),field('development_document'),lit(' от '),field('development_date'),lit(' № '),field('development_number'),lit(',')])
    b.edit(15,[lit('1. Ввести «'),field('system_short_name'),lit('» в эксплуатацию с '),field('commissioning_date'),lit('.')])
    b.edit(16,[lit('2. '),field('customer')])
    b.edit(17,[lit('обеспечить применение «'),field('system_short_name'),lit('» по следующему назначению: '),field('purpose'),lit('.')])
    b.edit(18,[lit('3. '),field('it_department'),lit(' обеспечить:')])
    for i in [19,20]: b.single(i,'system_short_name')
    b.person('responsible','Ответственный за исполнение'); b.single(21,'responsible'); b.signer(22,24)
    b.local_rule('is.date','blocking','Дата ввода не раньше даты готовности, кроме прямо предусмотренного и подтверждённого порядка.','Прим.8',when=both(present('readiness_date'),present('commissioning_date')),check=anyof(compare('date_gte','commissioning_date','readiness_date'),both(cond('date_exception'),present('date_exception_basis'))))
    b.local_rule('is.date_exception','manual_review','Проверьте документ и применимость исключения. Указанные пользователем реквизиты не означают фактическую проверку основания.','Прим.8',when=cond('date_exception'))
    b.local_rule('is.operating_docs','manual_review','Проверьте утверждение или принятие эксплуатационной документации до ввода либо одновременно с вводом. Указанные сведения не подтверждают исследование файла.','Прим.12',check=present('operating_docs_confirmation'))
    b.local_rule('is.evidence','manual_review','Сопоставьте наименования с технической и приёмочной документацией; проверьте документ о готовности и основания привлечения подрядчиков.','Прим.1–7, 9–15')
    b.route(3,'Приказы по вопросам информационных систем',['Юридическое подразделение','Департамент ДОУ','Департамент информационной безопасности','Департамент информационных технологий']); b.save()

    b=Builder('local-document','*об_утверждении*.docx',27,40,'local_document_order')
    for k,l in [('purpose','Цель принятия'),('document_name','Полное наименование документа'),('responsible_unit','Ответственное подразделение'),('location','Место размещения')]: b.f(k,l)
    b.f('document_type','Вид утверждаемого документа',type='choice',options=['Положение','Правила','Регламент','Инструкция','Порядок'])
    b.f('effective_date','Дата введения в действие',type='date'); b.f('transition','Переходный период',required=False)
    types=['Положение','Правила','Регламент','Инструкция','Порядок']
    def case(forms): return [branch(cond('document_type',key),[lit(value)]) for key,value in zip(types,forms)]
    name=[*case(['Положение','Правила','Регламент','Инструкцию','Порядок']),lit(' «'),field('document_name'),lit('»')]
    heading=[*case(['Положения','Правил','Регламента','Инструкции','Порядка']),lit(' «'),field('document_name'),lit('»')]
    b.edit(12,[lit('Об утверждении и введении в действие '),*heading]); b.single(13,'purpose')
    b.edit(15,[lit('1. Утвердить '),*name,lit(' согласно приложению к настоящему приказу.')])
    b.edit(16,[lit('2. Ввести указанный документ в действие с '),field('effective_date'),lit('.'),branch(present('transition'),[lit(' '),field('transition')])])
    b.single(17,'responsible_unit'); b.single(19,'location')
    b.repeal(20); b.control(21,5); b.numbering(15,16,17,20,21); b.signer(22,24)
    b.local_rule('local.attachment','warning','Ссылка на приложение сформирована, но сам локальный документ не включён в DOCX. Присоедините документ с тем же видом и названием и проверьте комплект.','Прим.2; общие правила')
    b.rule('local.attachment_review','manual_review','Проверьте приложение, совпадение названия и возможность подтвердить ознакомление работников. Комплектность автоматически не подтверждена.',INSTRUCTION('8.18; 10.2'))
    b.route(4,'Прочие приказы',['Юридическое подразделение','Департамент ДОУ']); b.save()

    b=Builder('records-responsibles','*назначении_ответственных*.docx',37,49,'records_responsibles_order')
    for k,l in [('basis','Нормативное основание'),('notification_recipient','Получатель уведомлений об изменениях'),('coordinator','Координирующее подразделение')]: b.f(k,l)
    b.single(13,'basis'); b.single(28,'notification_recipient'); b.single(29,'coordinator')
    items=[dict(key=k,label=l) for k,l in [('unit','Подразделение'),('position','Должность'),('name','Инициалы и фамилия')]]
    b.f('responsibles','Ответственные работники',type='repeater',item_fields=items)
    b.repeater('responsibles',2,1,3,[[field('unit')],[field('position'),lit(', '),field('name')]])
    b.repeal(30); b.control(31,6); b.numbering(15,24,28,29,30,31); b.signer(32,34)
    b.f('duties_consent_confirmed','Проверены согласие работников и соответствие функций должностям?',type='choice',required=False,options=['yes','no'])
    b.local_rule('records.consent','manual_review','Проверьте согласие каждого работника и соответствие обязанностей его должностным функциям; подтверждение пользователя не является полученным согласием.','Общие правила',check=cond('duties_consent_confirmed'))
    b.route(4,'Прочие приказы',['Юридическое подразделение','Департамент ДОУ']); b.save()

    b=Builder('property-transfer','*приеме_и_передаче*.docx',23,39,'property_transfer_order')
    b.f('basis','Основание передачи')
    for k,l in [('giver','Сдающее лицо/подразделение'),('receiver','Принимающее лицо/подразделение'),('representative','Представитель Организации')]: b.person(k,l)
    b.f('items','Состав передаваемых дел и имущества')
    b.f('include_source','Нужно отдельно указать источник передачи?',type='choice',options=['yes','no'])
    b.f('transfer_source','Источник передачи',required=False,required_if=cond('include_source'),visible_if=cond('include_source'))
    b.f('deadline_in_basis','Срок уже установлен локальным актом/документом-основанием?',type='choice',options=['yes','no'])
    b.f('deadline','Срок передачи',type='date',required=False,required_if=cond('deadline_in_basis','no'),visible_if=cond('deadline_in_basis','no'))
    b.f('deadline_basis','Реквизиты документа и положение о сроке',required=False,required_if=cond('deadline_in_basis'),visible_if=cond('deadline_in_basis'))
    b.single(13,'basis')
    constant=b.ps[15][b.ps[15].index(' в порядке, предусмотренном'):]
    b.edit(15,[lit('1. '),field('giver'),lit(' сдать, а '),field('receiver'),lit(' принять дела и имущество '),field('items'),choice('include_source',[lit(' от '),field('transfer_source')]),choice('deadline_in_basis',[],[lit(' в срок до '),field('deadline')]),lit(constant)])
    b.single(16,'representative'); b.control(17,3); b.signer(18,20)
    b.f('independence_required','Внутренние правила требуют независимого представителя?',type='choice',options=['yes','no','unknown'])
    b.f('independence_basis','Реквизиты требования независимости',required=False,required_if=cond('independence_required'),visible_if=cond('independence_required'))
    b.local_rule('transfer.independence','warning','Проверьте независимость представителя, если она требуется внутренними правилами; без подтверждённого правила универсальный запрет не применяется.','Общие правила',check=both(compare('field_not_equals','representative','giver'),compare('field_not_equals','representative','receiver')))
    b.local_rule('transfer.independence_confirmed_rule','blocking','При подтверждённом требовании независимости представитель должен отличаться от сдающего и принимающего. Проверка сравнивает введённые строки, фактическую личность проверьте вручную.','Общие правила',when=both(cond('independence_required'),present('independence_basis'),present('representative'),present('giver'),present('receiver')),check=both(compare('field_not_equals','representative','giver'),compare('field_not_equals','representative','receiver')))
    b.local_rule('transfer.attachment','manual_review','Если перечень ссылается на акт, подготовьте и присоедините его отдельно. Акт не включён автоматически. Проверьте срок, полномочия представителя и независимость по внутренним правилам.','Прим.4–6; общие правила')
    b.f('personnel_change','Передача связана с назначением, освобождением, реорганизацией или кадровыми изменениями?',type='choice',options=['yes','no','unknown'])
    b.f('access_changed','Передача связана с изменением доступа к ИС?',type='choice',options=['yes','no','unknown'])
    b.route(2,'Передача дел при кадровых изменениях',['Юридическое подразделение','Департамент ДОУ','Кадровое подразделение'],[dict(name='Департамент информационной безопасности',condition='Предоставление, изменение или прекращение доступа к ИС',when=cond('access_changed'))],cond('personnel_change'))
    b.route(4,'Прочие приказы',['Юридическое подразделение','Департамент ДОУ'],when=cond('personnel_change','no'))
    b.rule('transfer.route_review','manual_review','Уточните применимость внутренней строки 2 Матрицы: кадровое изменение и доступ к ИС. Неопределённость не означает отсутствие визирования.',MATRIX('Внутренние документы, строка 2'),when=anyof(cond('personnel_change','unknown'),cond('access_changed','unknown')))
    b.save()

    b=Builder('internal-audit','*внутреннего_аудита*.docx',36,50,'internal_audit_order')
    b.f('basis','Основание аудита с реквизитами'); b.f('processes','Официальные наименования процессов',type='text_list')
    for k,l in [('start_date','Дата начала'),('end_date','Дата окончания'),('report_deadline','Срок отчёта'),('plan_deadline','Срок плана мероприятий')]: b.f(k,l,type='date')
    for k,l in [('unit','Проверяемое подразделение, обеспечивающее доступ'),('report_addressee','Адресат отчёта'),('plan_owner','Ответственный за план мероприятий')]: b.f(k,l)
    b.person('leader','Руководитель аудиторской группы'); b.person('controller','Контролирующее лицо')
    b.f('access_scope','Уточнение доступа в пределах программы аудита',required=False)
    plural=dict(op='count_gt',field='processes',value=1)
    process=[branch(plural,[lit('процессов ')],[lit('процесса ')]),field('processes')]
    b.edit(12,[lit('О проведении внутреннего аудита '),*process]); b.single(13,'basis')
    b.edit(15,[lit('1. Провести с '),field('start_date'),lit(' по '),field('end_date'),lit(' внутренний аудит '),*process,lit('.')])
    b.edit(24,[field('leader')]); b.participants('auditors','Аудиторы',2,2,2)
    b.edit(27,[lit('4. '),field('unit'),lit(' обеспечить аудиторской группе доступ к документам, информационным системам и работникам в пределах программы аудита.'),branch(present('access_scope'),[lit(' '),field('access_scope')])])
    b.edit(28,[lit('5. Руководителю аудиторской группы до '),field('report_deadline'),lit(' представить '),field('report_addressee'),lit(' отчет о результатах аудита.')])
    b.edit(29,[lit('6. '),field('plan_owner'),lit(' до '),field('plan_deadline'),lit(' подготовить план мероприятий по результатам аудита.')])
    b.single(30,'controller'); b.signer(31,33)
    for id,a,c,description,section in [('audit.period','end_date','start_date','Дата начала не позже окончания','Прим.2'),('audit.report','report_deadline','end_date','Проверьте, что срок итогового отчёта не раньше окончания аудита','Прим.5'),('audit.plan','plan_deadline','report_deadline','Проверьте срок плана мероприятий относительно отчёта','Прим.6')]:
        b.local_rule(id,'blocking' if id=='audit.period' else 'warning',description,section,when=both(present(a),present(c)),check=compare('date_gte',a,c))
    b.f('independence_confirmed','Проверены компетентность и независимость аудиторов?',type='choice',required=False,options=['yes','no'])
    b.local_rule('audit.independence','manual_review','Проверьте компетентность и независимость каждого аудитора от проверяемой деятельности по фактическим обязанностям; генератор эти обязанности не знает.','Прим.3; общие правила',check=cond('independence_confirmed'))
    b.route(7,'Прочие распоряжения',['Юридическое подразделение','Департамент ДОУ']); b.save()


if __name__=='__main__': build()
